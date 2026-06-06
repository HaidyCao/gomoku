package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"wuziqi/internal/game"
	"wuziqi/internal/store"
)

type Server struct {
	store         *store.SQLiteStore
	staticDir     string
	config        Config
	rateLimiter   *rateLimiter
	createLimiter *rateLimiter
	logger        *slog.Logger
}

type Config struct {
	AllowedOrigins   []string
	MaxJSONBodyBytes int64
	RateLimit        RateLimitConfig
	// CreateRateLimit is a stricter, separate per-IP budget for POST /api/games
	// so unauthenticated game creation cannot be used to flood the database.
	CreateRateLimit RateLimitConfig
	// TrustProxyHeaders controls whether X-Forwarded-For / X-Real-IP are used to
	// derive the client IP. Enable ONLY when running behind a trusted reverse
	// proxy; otherwise clients could spoof these headers to evade rate limits.
	TrustProxyHeaders bool
}

type RateLimitConfig struct {
	Enabled           bool
	RequestsPerWindow int
	Window            time.Duration
}

const defaultMaxJSONBodyBytes int64 = 16 * 1024

type GameResponse struct {
	GameID          string                     `json:"gameId"`
	Mode            game.Mode                  `json:"mode"`
	BoardSize       int                        `json:"boardSize"`
	HumanColor      game.Color                 `json:"humanColor"`
	AgentColor      game.Color                 `json:"agentColor"`
	AgentState      AgentState                 `json:"agentState"`
	AgentStates     map[game.Player]AgentState `json:"agentStates"`
	HumanToken      string                     `json:"humanToken,omitempty"`
	AgentToken      string                     `json:"agentToken,omitempty"`
	AgentBlackToken string                     `json:"agentBlackToken,omitempty"`
	AgentWhiteToken string                     `json:"agentWhiteToken,omitempty"`
	NextTurn        game.Player                `json:"nextTurn,omitempty"`
	NextColor       game.Color                 `json:"nextColor,omitempty"`
	Status          game.Status                `json:"status"`
	EndReason       game.EndReason             `json:"endReason,omitempty"`
	Winner          game.Color                 `json:"winner,omitempty"`
	WinnerRole      game.Player                `json:"winnerRole,omitempty"`
	ResignedBy      game.Player                `json:"resignedBy,omitempty"`
	WinLine         []game.Point               `json:"winLine"`
	Board           [][]game.Color             `json:"board"`
	Moves           []game.Move                `json:"moves"`
	MoveCount       int                        `json:"moveCount"`
	CreatedAt       time.Time                  `json:"createdAt"`
	UpdatedAt       time.Time                  `json:"updatedAt"`
}

type AgentState struct {
	Joined        bool       `json:"joined"`
	Thinking      bool       `json:"thinking"`
	JoinedAt      *time.Time `json:"joinedAt,omitempty"`
	LastSeenAt    *time.Time `json:"lastSeenAt,omitempty"`
	ThinkingSince *time.Time `json:"thinkingSince,omitempty"`
}

type GameListItem struct {
	GameID      string                     `json:"gameId"`
	Mode        game.Mode                  `json:"mode"`
	HumanColor  game.Color                 `json:"humanColor"`
	AgentColor  game.Color                 `json:"agentColor"`
	AgentState  AgentState                 `json:"agentState"`
	AgentStates map[game.Player]AgentState `json:"agentStates"`
	NextTurn    game.Player                `json:"nextTurn,omitempty"`
	NextColor   game.Color                 `json:"nextColor,omitempty"`
	Status      game.Status                `json:"status"`
	EndReason   game.EndReason             `json:"endReason,omitempty"`
	Winner      game.Color                 `json:"winner,omitempty"`
	WinnerRole  game.Player                `json:"winnerRole,omitempty"`
	ResignedBy  game.Player                `json:"resignedBy,omitempty"`
	MoveCount   int                        `json:"moveCount"`
	CreatedAt   time.Time                  `json:"createdAt"`
	UpdatedAt   time.Time                  `json:"updatedAt"`
}

func New(store *store.SQLiteStore, staticDir string) *Server {
	return NewWithConfig(store, staticDir, Config{})
}

func NewWithConfig(store *store.SQLiteStore, staticDir string, config Config) *Server {
	config = normalizeConfig(config)
	return &Server{
		store:         store,
		staticDir:     staticDir,
		config:        config,
		rateLimiter:   newRateLimiter(config.RateLimit),
		createLimiter: newRateLimiter(config.CreateRateLimit),
		logger:        slog.Default(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/games", s.handleGames)
	mux.HandleFunc("/api/games/", s.handleGame)
	mux.Handle("/", spaHandler(s.staticDir))
	return s.requestLog(s.securityHeaders(s.cors(s.rateLimit(mux))))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGames(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateGame(w, r)
	case http.MethodGet:
		s.handleListGames(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleCreateGame(w http.ResponseWriter, r *http.Request) {
	if s.createLimiter != nil && !s.createLimiter.allow(s.clientIP(r), time.Now()) {
		writeError(w, http.StatusTooManyRequests, "too many new games from this client, slow down")
		return
	}
	var request struct {
		Mode       string `json:"mode"`
		HumanColor string `json:"humanColor"`
	}
	if r.Body != nil {
		defer r.Body.Close()
	}
	if err := decodeJSON(w, r, s.config.MaxJSONBodyBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	mode, err := game.NormalizeMode(request.Mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if request.HumanColor == "" {
		request.HumanColor = string(game.Black)
	}

	humanColor := game.Empty
	if mode == game.ModeHumanAgent {
		humanColor, err = game.NormalizeColor(request.HumanColor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	g, err := s.store.CreateGame(r.Context(), mode, humanColor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create game failed")
		return
	}
	writeJSON(w, http.StatusCreated, newGameResponse(g, true))
}

func (s *Server) handleListGames(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be a number")
			return
		}
		limit = parsed
	}

	games, err := s.store.ListGames(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list games failed")
		return
	}

	response := make([]GameListItem, 0, len(games))
	for _, g := range games {
		response = append(response, newGameListItem(g))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGame(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/games/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "game not found")
		return
	}

	gameID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		s.handleGetGame(w, r, gameID)
		return
	}
	if len(parts) == 2 && parts[1] == "moves" && r.Method == http.MethodPost {
		s.handleMove(w, r, gameID)
		return
	}
	if len(parts) == 2 && parts[1] == "resign" && r.Method == http.MethodPost {
		s.handleResign(w, r, gameID)
		return
	}
	if len(parts) == 3 && parts[1] == "agent" && parts[2] == "join" && r.Method == http.MethodPost {
		s.handleAgentJoin(w, r, gameID)
		return
	}
	if len(parts) == 3 && parts[1] == "agent" && parts[2] == "status" && r.Method == http.MethodPost {
		s.handleAgentStatus(w, r, gameID)
		return
	}
	writeError(w, http.StatusNotFound, "route not found")
}

func (s *Server) handleGetGame(w http.ResponseWriter, r *http.Request, gameID string) {
	g, err := s.store.GetGame(r.Context(), gameID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newGameResponse(g, false))
}

func (s *Server) handleResign(w http.ResponseWriter, r *http.Request, gameID string) {
	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}

	g, err := s.store.Resign(r.Context(), gameID, token)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newGameResponse(g, false))
}

func (s *Server) handleAgentJoin(w http.ResponseWriter, r *http.Request, gameID string) {
	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}

	g, err := s.store.MarkAgentJoined(r.Context(), gameID, token)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newGameResponse(g, false))
}

func (s *Server) handleAgentStatus(w http.ResponseWriter, r *http.Request, gameID string) {
	var request struct {
		Thinking bool `json:"thinking"`
	}
	defer r.Body.Close()
	if err := decodeJSON(w, r, s.config.MaxJSONBodyBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}

	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}

	g, err := s.store.MarkAgentThinking(r.Context(), gameID, token, request.Thinking)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newGameResponse(g, false))
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request, gameID string) {
	var request struct {
		Row int `json:"row"`
		Col int `json:"col"`
	}
	defer r.Body.Close()
	if err := decodeJSON(w, r, s.config.MaxJSONBodyBytes, &request); err != nil {
		writeDecodeError(w, err)
		return
	}

	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}

	g, err := s.store.ApplyMove(r.Context(), gameID, token, request.Row, request.Col)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newGameResponse(g, false))
}

func newGameResponse(g game.Game, includeTokens bool) GameResponse {
	response := GameResponse{
		GameID:      g.ID,
		Mode:        g.NormalizedMode(),
		BoardSize:   game.BoardSize,
		HumanColor:  g.HumanColor,
		AgentColor:  g.AgentColor,
		AgentState:  newAgentState(g),
		AgentStates: newAgentStates(g),
		NextTurn:    g.NextPlayer(),
		NextColor:   g.NextColor,
		Status:      g.Status,
		EndReason:   g.EndReason,
		Winner:      g.WinnerColor,
		WinnerRole:  g.WinnerPlayer(),
		ResignedBy:  resignedBy(g),
		WinLine:     g.WinLine,
		Board:       game.BuildBoard(g.Moves),
		Moves:       g.Moves,
		MoveCount:   g.MoveCount,
		CreatedAt:   g.CreatedAt,
		UpdatedAt:   g.UpdatedAt,
	}
	if response.WinLine == nil {
		response.WinLine = []game.Point{}
	}
	if response.Moves == nil {
		response.Moves = []game.Move{}
	}
	if includeTokens {
		response.HumanToken = g.HumanToken
		response.AgentToken = g.AgentToken
		response.AgentBlackToken = g.AgentBlackToken
		response.AgentWhiteToken = g.AgentWhiteToken
	}
	return response
}

func newGameListItem(g game.Game) GameListItem {
	return GameListItem{
		GameID:      g.ID,
		Mode:        g.NormalizedMode(),
		HumanColor:  g.HumanColor,
		AgentColor:  g.AgentColor,
		AgentState:  newAgentState(g),
		AgentStates: newAgentStates(g),
		NextTurn:    g.NextPlayer(),
		NextColor:   g.NextColor,
		Status:      g.Status,
		EndReason:   g.EndReason,
		Winner:      g.WinnerColor,
		WinnerRole:  g.WinnerPlayer(),
		ResignedBy:  resignedBy(g),
		MoveCount:   g.MoveCount,
		CreatedAt:   g.CreatedAt,
		UpdatedAt:   g.UpdatedAt,
	}
}

func resignedBy(g game.Game) game.Player {
	if g.EndReason != game.EndReasonResignation || g.WinnerColor == game.Empty {
		return ""
	}
	return g.PlayerForColor(game.Opposite(g.WinnerColor))
}

func newAgentState(g game.Game) AgentState {
	return AgentState{
		Joined:        g.AgentJoinedAt != nil,
		Thinking:      g.AgentThinking,
		JoinedAt:      g.AgentJoinedAt,
		LastSeenAt:    g.AgentLastSeenAt,
		ThinkingSince: g.AgentThinkingSince,
	}
}

func newAgentStates(g game.Game) map[game.Player]AgentState {
	if g.NormalizedMode() == game.ModeAgentAgent {
		return map[game.Player]AgentState{
			game.AgentBlack: agentStateForPlayer(g, game.AgentBlack),
			game.AgentWhite: agentStateForPlayer(g, game.AgentWhite),
		}
	}
	return map[game.Player]AgentState{
		game.Agent: newAgentState(g),
	}
}

func agentStateForPlayer(g game.Game, player game.Player) AgentState {
	switch player {
	case game.AgentBlack:
		return AgentState{
			Joined:        g.AgentBlackJoinedAt != nil,
			Thinking:      g.AgentBlackThinking,
			JoinedAt:      g.AgentBlackJoinedAt,
			LastSeenAt:    g.AgentBlackLastSeenAt,
			ThinkingSince: g.AgentBlackThinkingSince,
		}
	case game.AgentWhite:
		return AgentState{
			Joined:        g.AgentWhiteJoinedAt != nil,
			Thinking:      g.AgentWhiteThinking,
			JoinedAt:      g.AgentWhiteJoinedAt,
			LastSeenAt:    g.AgentWhiteLastSeenAt,
			ThinkingSince: g.AgentWhiteThinkingSince,
		}
	default:
		return newAgentState(g)
	}
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return ""
	}
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func handleStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "game not found")
	case errors.Is(err, store.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "invalid game token")
	case errors.Is(err, game.ErrOutOfBounds), errors.Is(err, game.ErrUnknownPlayer):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, game.ErrWrongTurn), errors.Is(err, game.ErrCellOccupied), errors.Is(err, game.ErrGameOver):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "request failed")
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, target any) error {
	if maxBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("JSON body must be at most %d bytes", maxBytesError.Limit))
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON body")
}

func (s *Server) cors(next http.Handler) http.Handler {
	allowedOrigins := originSet(s.config.AllowedOrigins)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			if origin != "" && !allowedOrigins[origin] {
				writeError(w, http.StatusForbidden, "origin not allowed")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.rateLimiter != nil && shouldRateLimit(r) {
			if !s.rateLimiter.allow(s.clientIP(r), time.Now()) {
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status code so the request logger can
// report it. It defaults to 200, matching net/http's implicit status.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.wrote = true
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if !rec.wrote {
		rec.wrote = true
	}
	return rec.ResponseWriter.Write(b)
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		switch {
		case r.URL.Path == "/api/health":
			level = slog.LevelDebug
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status >= 400:
			level = slog.LevelWarn
		}
		s.logger.LogAttrs(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
			slog.String("ip", s.clientIP(r)),
		)
	})
}

func spaHandler(staticDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if staticDir == "" {
			writeError(w, http.StatusNotFound, "frontend build not found")
			return
		}

		requestPath := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if requestPath == "." {
			requestPath = "index.html"
		}

		filePath := filepath.Join(staticDir, requestPath)
		if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
			http.ServeFile(w, r, filePath)
			return
		}

		indexPath := filepath.Join(staticDir, "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			writeError(w, http.StatusNotFound, "frontend build not found")
			return
		}
		http.ServeFile(w, r, indexPath)
	})
}

func normalizeConfig(config Config) Config {
	if config.MaxJSONBodyBytes <= 0 {
		config.MaxJSONBodyBytes = defaultMaxJSONBodyBytes
	}
	if config.RateLimit.RequestsPerWindow <= 0 {
		config.RateLimit.RequestsPerWindow = 300
	}
	if config.RateLimit.Window <= 0 {
		config.RateLimit.Window = time.Minute
	}
	if config.CreateRateLimit.RequestsPerWindow <= 0 {
		config.CreateRateLimit.RequestsPerWindow = 10
	}
	if config.CreateRateLimit.Window <= 0 {
		config.CreateRateLimit.Window = time.Minute
	}
	return config
}

func originSet(origins []string) map[string]bool {
	set := make(map[string]bool, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			set[origin] = true
		}
	}
	return set
}

func shouldRateLimit(r *http.Request) bool {
	if r.Method == http.MethodOptions || r.URL.Path == "/api/health" {
		return false
	}
	if r.URL.Path == "/api/games" {
		return r.Method == http.MethodPost || r.Method == http.MethodGet
	}
	if !strings.HasPrefix(r.URL.Path, "/api/games/") {
		return false
	}
	if r.Method != http.MethodPost {
		return false
	}
	return strings.HasSuffix(r.URL.Path, "/moves") || strings.HasSuffix(r.URL.Path, "/agent/status")
}

// clientIP derives the client address used for rate limiting and logging. It
// only honours proxy-supplied headers when TrustProxyHeaders is set, so a
// directly-exposed server cannot be tricked into trusting a spoofed
// X-Forwarded-For / X-Real-IP.
func (s *Server) clientIP(r *http.Request) string {
	if s.config.TrustProxyHeaders {
		// Cloudflare sets CF-Connecting-IP to the real visitor IP. Prefer it:
		// unlike X-Forwarded-For (whose first hop is client-spoofable), it is a
		// single trusted value, and behind a tunnel the origin cannot be reached
		// directly to forge it.
		if cfIP := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cfIP != "" {
			return cfIP
		}
		if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
			if first := strings.TrimSpace(strings.Split(forwardedFor, ",")[0]); first != "" {
				return first
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}
	host := r.RemoteAddr
	if index := strings.LastIndex(host, ":"); index > -1 {
		return host[:index]
	}
	return host
}

type rateLimiter struct {
	config RateLimitConfig
	mu     sync.Mutex
	hits   map[string]rateLimitWindow
}

type rateLimitWindow struct {
	count   int
	resetAt time.Time
}

func newRateLimiter(config RateLimitConfig) *rateLimiter {
	if !config.Enabled {
		return nil
	}
	return &rateLimiter{
		config: config,
		hits:   make(map[string]rateLimitWindow),
	}
}

func (l *rateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	window := l.hits[key]
	if window.resetAt.IsZero() || !now.Before(window.resetAt) {
		l.hits[key] = rateLimitWindow{count: 1, resetAt: now.Add(l.config.Window)}
		l.cleanup(now)
		return true
	}
	if window.count >= l.config.RequestsPerWindow {
		return false
	}
	window.count++
	l.hits[key] = window
	return true
}

func (l *rateLimiter) cleanup(now time.Time) {
	if len(l.hits) < 1024 {
		return
	}
	for key, window := range l.hits {
		if !now.Before(window.resetAt) {
			delete(l.hits, key)
		}
	}
}
