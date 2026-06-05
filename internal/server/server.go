package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wuziqi/internal/game"
	"wuziqi/internal/store"
)

type Server struct {
	store     *store.SQLiteStore
	staticDir string
}

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
	return &Server{
		store:     store,
		staticDir: staticDir,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/games", s.handleGames)
	mux.HandleFunc("/api/games/", s.handleGame)
	mux.Handle("/", spaHandler(s.staticDir))
	return cors(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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
	var request struct {
		Mode       string `json:"mode"`
		HumanColor string `json:"humanColor"`
	}
	if r.Body != nil {
		defer r.Body.Close()
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
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
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
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
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
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

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
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
