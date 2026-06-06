package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"wuziqi/internal/game"
)

var (
	ErrNotFound     = errors.New("game not found")
	ErrUnauthorized = errors.New("invalid game token")
)

type SQLiteStore struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(ctx context.Context, path string) (*SQLiteStore, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	store := &SQLiteStore{db: db}
	if err := store.configure(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Checkpoint(ctx)
	return s.db.Close()
}

// Ping verifies database connectivity. It is safe for concurrent use and
// honours ctx cancellation, so it can back a health endpoint without blocking
// on the store mutex.
func (s *SQLiteStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Checkpoint flushes the write-ahead log back into the main database file and
// truncates it. It runs on graceful shutdown so a restart does not inherit a
// large .db-wal file.
func (s *SQLiteStore) Checkpoint(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func (s *SQLiteStore) configure(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA wal_autocheckpoint = 1000`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS games (
	id TEXT PRIMARY KEY,
	mode TEXT NOT NULL DEFAULT 'human-agent',
	human_token TEXT NOT NULL,
	agent_token TEXT NOT NULL,
	agent_black_token TEXT NOT NULL DEFAULT '',
	agent_white_token TEXT NOT NULL DEFAULT '',
	human_color TEXT NOT NULL,
	agent_color TEXT NOT NULL,
	forbidden INTEGER NOT NULL DEFAULT 0,
	agent_strategy TEXT NOT NULL DEFAULT 'think',
	owner_id TEXT NOT NULL DEFAULT '',
	next_color TEXT NOT NULL,
	status TEXT NOT NULL,
	end_reason TEXT NOT NULL DEFAULT '',
	winner_color TEXT NOT NULL DEFAULT '',
	win_line_json TEXT NOT NULL DEFAULT '[]',
	agent_joined_at TEXT NOT NULL DEFAULT '',
	agent_last_seen_at TEXT NOT NULL DEFAULT '',
	agent_thinking INTEGER NOT NULL DEFAULT 0,
	agent_thinking_since TEXT NOT NULL DEFAULT '',
	agent_black_joined_at TEXT NOT NULL DEFAULT '',
	agent_black_last_seen_at TEXT NOT NULL DEFAULT '',
	agent_black_thinking INTEGER NOT NULL DEFAULT 0,
	agent_black_thinking_since TEXT NOT NULL DEFAULT '',
	agent_white_joined_at TEXT NOT NULL DEFAULT '',
	agent_white_last_seen_at TEXT NOT NULL DEFAULT '',
	agent_white_thinking INTEGER NOT NULL DEFAULT 0,
	agent_white_thinking_since TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS moves (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	game_id TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
	move_number INTEGER NOT NULL,
	row INTEGER NOT NULL,
	col INTEGER NOT NULL,
	color TEXT NOT NULL,
	player TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(game_id, move_number),
	UNIQUE(game_id, row, col)
);

CREATE INDEX IF NOT EXISTS idx_games_updated_at ON games(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_moves_game_number ON moves(game_id, move_number);
`)
	if err != nil {
		return err
	}
	if err := s.ensureGameColumns(ctx); err != nil {
		return err
	}
	// Created after ensureGameColumns so the owner_id column is guaranteed to
	// exist on databases migrated from an older schema.
	_, err = s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_games_owner_updated ON games(owner_id, updated_at DESC)`)
	return err
}

func (s *SQLiteStore) ensureGameColumns(ctx context.Context) error {
	columns, err := s.gameColumns(ctx)
	if err != nil {
		return err
	}
	statements := map[string]string{
		"mode":                       `ALTER TABLE games ADD COLUMN mode TEXT NOT NULL DEFAULT 'human-agent'`,
		"agent_black_token":          `ALTER TABLE games ADD COLUMN agent_black_token TEXT NOT NULL DEFAULT ''`,
		"agent_white_token":          `ALTER TABLE games ADD COLUMN agent_white_token TEXT NOT NULL DEFAULT ''`,
		"agent_joined_at":            `ALTER TABLE games ADD COLUMN agent_joined_at TEXT NOT NULL DEFAULT ''`,
		"agent_last_seen_at":         `ALTER TABLE games ADD COLUMN agent_last_seen_at TEXT NOT NULL DEFAULT ''`,
		"agent_thinking":             `ALTER TABLE games ADD COLUMN agent_thinking INTEGER NOT NULL DEFAULT 0`,
		"agent_thinking_since":       `ALTER TABLE games ADD COLUMN agent_thinking_since TEXT NOT NULL DEFAULT ''`,
		"agent_black_joined_at":      `ALTER TABLE games ADD COLUMN agent_black_joined_at TEXT NOT NULL DEFAULT ''`,
		"agent_black_last_seen_at":   `ALTER TABLE games ADD COLUMN agent_black_last_seen_at TEXT NOT NULL DEFAULT ''`,
		"agent_black_thinking":       `ALTER TABLE games ADD COLUMN agent_black_thinking INTEGER NOT NULL DEFAULT 0`,
		"agent_black_thinking_since": `ALTER TABLE games ADD COLUMN agent_black_thinking_since TEXT NOT NULL DEFAULT ''`,
		"agent_white_joined_at":      `ALTER TABLE games ADD COLUMN agent_white_joined_at TEXT NOT NULL DEFAULT ''`,
		"agent_white_last_seen_at":   `ALTER TABLE games ADD COLUMN agent_white_last_seen_at TEXT NOT NULL DEFAULT ''`,
		"agent_white_thinking":       `ALTER TABLE games ADD COLUMN agent_white_thinking INTEGER NOT NULL DEFAULT 0`,
		"agent_white_thinking_since": `ALTER TABLE games ADD COLUMN agent_white_thinking_since TEXT NOT NULL DEFAULT ''`,
		"end_reason":                 `ALTER TABLE games ADD COLUMN end_reason TEXT NOT NULL DEFAULT ''`,
		"forbidden":                  `ALTER TABLE games ADD COLUMN forbidden INTEGER NOT NULL DEFAULT 0`,
		"agent_strategy":             `ALTER TABLE games ADD COLUMN agent_strategy TEXT NOT NULL DEFAULT 'think'`,
		"owner_id":                   `ALTER TABLE games ADD COLUMN owner_id TEXT NOT NULL DEFAULT ''`,
	}
	for column, statement := range statements {
		if columns[column] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) gameColumns(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(games)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

// CreateOptions captures everything chosen in the pre-game setup wizard. Mode
// defaults to human-agent; AgentStrategy is normalized; OwnerID is the anonymous
// per-browser identifier used to scope "my games" in the history list.
type CreateOptions struct {
	Mode          game.Mode
	HumanColor    game.Color
	Forbidden     bool
	AgentStrategy string
	OwnerID       string
}

func (s *SQLiteStore) CreateGame(ctx context.Context, opts CreateOptions) (game.Game, error) {
	mode := opts.Mode
	if mode == "" {
		mode = game.ModeHumanAgent
	}
	now := time.Now().UTC()

	id, err := game.NewGameID()
	if err != nil {
		return game.Game{}, err
	}

	g := game.Game{
		ID:            id,
		Mode:          mode,
		Forbidden:     opts.Forbidden,
		AgentStrategy: game.NormalizeStrategy(opts.AgentStrategy),
		OwnerID:       opts.OwnerID,
		NextColor:     game.Black,
		Status:        game.StatusPlaying,
		EndReason:     game.EndReasonNone,
		WinnerColor:   game.Empty,
		WinLine:       []game.Point{},
		Moves:         []game.Move{},
		MoveCount:     0,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if g.Mode == game.ModeAgentAgent {
		g.AgentBlackToken, err = game.NewToken()
		if err != nil {
			return game.Game{}, err
		}
		g.AgentWhiteToken, err = game.NewToken()
		if err != nil {
			return game.Game{}, err
		}
	} else {
		g.HumanColor = opts.HumanColor
		g.AgentColor = game.Opposite(opts.HumanColor)
		g.HumanToken, err = game.NewToken()
		if err != nil {
			return game.Game{}, err
		}
		g.AgentToken, err = game.NewToken()
		if err != nil {
			return game.Game{}, err
		}
	}

	winLine, err := json.Marshal(g.WinLine)
	if err != nil {
		return game.Game{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO games (
	id, mode, human_token, agent_token, agent_black_token, agent_white_token,
	human_color, agent_color, forbidden, agent_strategy, owner_id, next_color,
	status, end_reason, winner_color, win_line_json, agent_joined_at, agent_last_seen_at,
	agent_thinking, agent_thinking_since, agent_black_joined_at, agent_black_last_seen_at,
	agent_black_thinking, agent_black_thinking_since, agent_white_joined_at, agent_white_last_seen_at,
	agent_white_thinking, agent_white_thinking_since, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ID,
		g.Mode,
		g.HumanToken,
		g.AgentToken,
		g.AgentBlackToken,
		g.AgentWhiteToken,
		g.HumanColor,
		g.AgentColor,
		boolInt(g.Forbidden),
		g.AgentStrategy,
		g.OwnerID,
		g.NextColor,
		g.Status,
		g.EndReason,
		g.WinnerColor,
		string(winLine),
		formatOptionalTime(g.AgentJoinedAt),
		formatOptionalTime(g.AgentLastSeenAt),
		boolInt(g.AgentThinking),
		formatOptionalTime(g.AgentThinkingSince),
		formatOptionalTime(g.AgentBlackJoinedAt),
		formatOptionalTime(g.AgentBlackLastSeenAt),
		boolInt(g.AgentBlackThinking),
		formatOptionalTime(g.AgentBlackThinkingSince),
		formatOptionalTime(g.AgentWhiteJoinedAt),
		formatOptionalTime(g.AgentWhiteLastSeenAt),
		boolInt(g.AgentWhiteThinking),
		formatOptionalTime(g.AgentWhiteThinkingSince),
		formatTime(g.CreatedAt),
		formatTime(g.UpdatedAt),
	)
	if err != nil {
		return game.Game{}, err
	}
	return g, nil
}

func (s *SQLiteStore) ListGames(ctx context.Context, limit int) ([]game.Game, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT
	g.id, g.mode, g.human_token, g.agent_token, g.agent_black_token, g.agent_white_token,
	g.human_color, g.agent_color, g.forbidden, g.agent_strategy, g.owner_id,
	g.next_color, g.status, g.end_reason, g.winner_color, g.win_line_json,
	g.agent_joined_at, g.agent_last_seen_at, g.agent_thinking, g.agent_thinking_since,
	g.agent_black_joined_at, g.agent_black_last_seen_at, g.agent_black_thinking,
	g.agent_black_thinking_since, g.agent_white_joined_at, g.agent_white_last_seen_at,
	g.agent_white_thinking, g.agent_white_thinking_since,
	g.created_at, g.updated_at, COUNT(m.id) AS move_count
FROM games g
LEFT JOIN moves m ON m.game_id = g.id
GROUP BY g.id
ORDER BY g.updated_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	games := make([]game.Game, 0)
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

// ListGamesByOwner returns the most recently updated games created by the given
// anonymous owner, newest first. It backs the "我的对局" view; the unfiltered
// ListGames backs "全部对局".
func (s *SQLiteStore) ListGamesByOwner(ctx context.Context, ownerID string, limit int) ([]game.Game, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT
	g.id, g.mode, g.human_token, g.agent_token, g.agent_black_token, g.agent_white_token,
	g.human_color, g.agent_color, g.forbidden, g.agent_strategy, g.owner_id,
	g.next_color, g.status, g.end_reason, g.winner_color, g.win_line_json,
	g.agent_joined_at, g.agent_last_seen_at, g.agent_thinking, g.agent_thinking_since,
	g.agent_black_joined_at, g.agent_black_last_seen_at, g.agent_black_thinking,
	g.agent_black_thinking_since, g.agent_white_joined_at, g.agent_white_last_seen_at,
	g.agent_white_thinking, g.agent_white_thinking_since,
	g.created_at, g.updated_at, COUNT(m.id) AS move_count
FROM games g
LEFT JOIN moves m ON m.game_id = g.id
WHERE g.owner_id = ?
GROUP BY g.id
ORDER BY g.updated_at DESC
LIMIT ?`, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	games := make([]game.Game, 0)
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

// PurgeStaleGames deletes finished games whose last activity predates
// finishedOlderThan, and abandoned in-progress games whose last activity
// predates abandonedOlderThan. Matching rows in moves are removed via the
// games.id ON DELETE CASCADE. A non-positive duration disables that category.
// It returns the number of games deleted.
func (s *SQLiteStore) PurgeStaleGames(ctx context.Context, finishedOlderThan, abandonedOlderThan time.Duration) (int64, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	var deleted int64
	if finishedOlderThan > 0 {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM games WHERE status != ? AND updated_at < ?`,
			game.StatusPlaying, formatTime(now.Add(-finishedOlderThan)))
		if err != nil {
			return deleted, err
		}
		if n, err := res.RowsAffected(); err == nil {
			deleted += n
		}
	}
	if abandonedOlderThan > 0 {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM games WHERE status = ? AND updated_at < ?`,
			game.StatusPlaying, formatTime(now.Add(-abandonedOlderThan)))
		if err != nil {
			return deleted, err
		}
		if n, err := res.RowsAffected(); err == nil {
			deleted += n
		}
	}
	return deleted, nil
}

func (s *SQLiteStore) GetGame(ctx context.Context, id string) (game.Game, error) {
	g, err := s.getGame(ctx, id)
	if err != nil {
		return game.Game{}, err
	}
	moves, err := s.getMoves(ctx, id)
	if err != nil {
		return game.Game{}, err
	}
	g.Moves = moves
	g.MoveCount = len(moves)
	return g, nil
}

func (s *SQLiteStore) MarkAgentJoined(ctx context.Context, id string, token string) (game.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, err := s.getGame(ctx, id)
	if err != nil {
		return game.Game{}, err
	}
	player, err := playerFromToken(g, token)
	if err != nil {
		return game.Game{}, err
	}
	if !game.IsAgentPlayer(player) {
		return game.Game{}, ErrUnauthorized
	}

	now := time.Now().UTC()
	markAgentJoined(&g, player, now)
	g.UpdatedAt = now

	if err := s.updateAgentState(ctx, g); err != nil {
		return game.Game{}, err
	}
	return s.GetGame(ctx, id)
}

func (s *SQLiteStore) MarkAgentThinking(ctx context.Context, id string, token string, thinking bool) (game.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, err := s.getGame(ctx, id)
	if err != nil {
		return game.Game{}, err
	}
	player, err := playerFromToken(g, token)
	if err != nil {
		return game.Game{}, err
	}
	if !game.IsAgentPlayer(player) {
		return game.Game{}, ErrUnauthorized
	}

	now := time.Now().UTC()
	markAgentThinking(&g, player, now, thinking)
	g.UpdatedAt = now

	if err := s.updateAgentState(ctx, g); err != nil {
		return game.Game{}, err
	}
	return s.GetGame(ctx, id)
}

func (s *SQLiteStore) Resign(ctx context.Context, id string, token string) (game.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, err := s.getGame(ctx, id)
	if err != nil {
		return game.Game{}, err
	}
	moves, err := s.getMoves(ctx, id)
	if err != nil {
		return game.Game{}, err
	}
	g.Moves = moves
	g.MoveCount = len(moves)

	player, err := playerFromToken(g, token)
	if err != nil {
		return game.Game{}, err
	}

	now := time.Now().UTC()
	if err := game.Resign(&g, player, now); err != nil {
		return game.Game{}, err
	}
	if game.IsAgentPlayer(player) {
		markAgentJoined(&g, player, now)
	}

	if err := s.updateGameState(ctx, g); err != nil {
		return game.Game{}, err
	}
	return s.GetGame(ctx, id)
}

func (s *SQLiteStore) ApplyMove(ctx context.Context, id string, token string, row int, col int) (game.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, err := s.getGame(ctx, id)
	if err != nil {
		return game.Game{}, err
	}
	moves, err := s.getMoves(ctx, id)
	if err != nil {
		return game.Game{}, err
	}
	g.Moves = moves
	g.MoveCount = len(moves)

	player, err := playerFromToken(g, token)
	if err != nil {
		return game.Game{}, err
	}

	now := time.Now().UTC()
	move, err := game.ApplyMove(&g, row, col, player, now)
	if err != nil {
		return game.Game{}, err
	}
	if game.IsAgentPlayer(player) {
		markAgentJoined(&g, player, now)
	}

	winLine, err := json.Marshal(g.WinLine)
	if err != nil {
		return game.Game{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return game.Game{}, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
INSERT INTO moves (game_id, move_number, row, col, color, player, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		g.ID,
		move.MoveNumber,
		move.Row,
		move.Col,
		move.Color,
		move.Player,
		formatTime(move.CreatedAt),
	)
	if err != nil {
		return game.Game{}, err
	}

	_, err = tx.ExecContext(ctx, `
UPDATE games
SET next_color = ?, status = ?, end_reason = ?, winner_color = ?, win_line_json = ?,
	agent_joined_at = ?, agent_last_seen_at = ?, agent_thinking = ?, agent_thinking_since = ?,
	agent_black_joined_at = ?, agent_black_last_seen_at = ?, agent_black_thinking = ?, agent_black_thinking_since = ?,
	agent_white_joined_at = ?, agent_white_last_seen_at = ?, agent_white_thinking = ?, agent_white_thinking_since = ?,
	updated_at = ?
WHERE id = ?`,
		g.NextColor,
		g.Status,
		g.EndReason,
		g.WinnerColor,
		string(winLine),
		formatOptionalTime(g.AgentJoinedAt),
		formatOptionalTime(g.AgentLastSeenAt),
		boolInt(g.AgentThinking),
		formatOptionalTime(g.AgentThinkingSince),
		formatOptionalTime(g.AgentBlackJoinedAt),
		formatOptionalTime(g.AgentBlackLastSeenAt),
		boolInt(g.AgentBlackThinking),
		formatOptionalTime(g.AgentBlackThinkingSince),
		formatOptionalTime(g.AgentWhiteJoinedAt),
		formatOptionalTime(g.AgentWhiteLastSeenAt),
		boolInt(g.AgentWhiteThinking),
		formatOptionalTime(g.AgentWhiteThinkingSince),
		formatTime(g.UpdatedAt),
		g.ID,
	)
	if err != nil {
		return game.Game{}, err
	}

	if err := tx.Commit(); err != nil {
		return game.Game{}, err
	}
	return g, nil
}

func (s *SQLiteStore) getGame(ctx context.Context, id string) (game.Game, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT
	id, mode, human_token, agent_token, agent_black_token, agent_white_token,
	human_color, agent_color, forbidden, agent_strategy, owner_id,
	next_color, status, end_reason, winner_color, win_line_json,
	agent_joined_at, agent_last_seen_at, agent_thinking, agent_thinking_since,
	agent_black_joined_at, agent_black_last_seen_at, agent_black_thinking,
	agent_black_thinking_since, agent_white_joined_at, agent_white_last_seen_at,
	agent_white_thinking, agent_white_thinking_since,
	created_at, updated_at, 0 AS move_count
FROM games
WHERE id = ?`, id)

	g, err := scanGame(row)
	if errors.Is(err, sql.ErrNoRows) {
		return game.Game{}, ErrNotFound
	}
	return g, err
}

func (s *SQLiteStore) getMoves(ctx context.Context, gameID string) ([]game.Move, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT move_number, row, col, color, player, created_at
FROM moves
WHERE game_id = ?
ORDER BY move_number ASC`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	moves := make([]game.Move, 0)
	for rows.Next() {
		var move game.Move
		var createdAt string
		if err := rows.Scan(
			&move.MoveNumber,
			&move.Row,
			&move.Col,
			&move.Color,
			&move.Player,
			&createdAt,
		); err != nil {
			return nil, err
		}
		move.CreatedAt = parseTime(createdAt)
		moves = append(moves, move)
	}
	return moves, rows.Err()
}

func (s *SQLiteStore) updateAgentState(ctx context.Context, g game.Game) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE games
SET agent_joined_at = ?, agent_last_seen_at = ?, agent_thinking = ?, agent_thinking_since = ?,
	agent_black_joined_at = ?, agent_black_last_seen_at = ?, agent_black_thinking = ?, agent_black_thinking_since = ?,
	agent_white_joined_at = ?, agent_white_last_seen_at = ?, agent_white_thinking = ?, agent_white_thinking_since = ?,
	updated_at = ?
WHERE id = ?`,
		formatOptionalTime(g.AgentJoinedAt),
		formatOptionalTime(g.AgentLastSeenAt),
		boolInt(g.AgentThinking),
		formatOptionalTime(g.AgentThinkingSince),
		formatOptionalTime(g.AgentBlackJoinedAt),
		formatOptionalTime(g.AgentBlackLastSeenAt),
		boolInt(g.AgentBlackThinking),
		formatOptionalTime(g.AgentBlackThinkingSince),
		formatOptionalTime(g.AgentWhiteJoinedAt),
		formatOptionalTime(g.AgentWhiteLastSeenAt),
		boolInt(g.AgentWhiteThinking),
		formatOptionalTime(g.AgentWhiteThinkingSince),
		formatTime(g.UpdatedAt),
		g.ID,
	)
	return err
}

func (s *SQLiteStore) updateGameState(ctx context.Context, g game.Game) error {
	winLine, err := json.Marshal(g.WinLine)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE games
SET next_color = ?, status = ?, end_reason = ?, winner_color = ?, win_line_json = ?,
	agent_joined_at = ?, agent_last_seen_at = ?, agent_thinking = ?, agent_thinking_since = ?,
	agent_black_joined_at = ?, agent_black_last_seen_at = ?, agent_black_thinking = ?, agent_black_thinking_since = ?,
	agent_white_joined_at = ?, agent_white_last_seen_at = ?, agent_white_thinking = ?, agent_white_thinking_since = ?,
	updated_at = ?
WHERE id = ?`,
		g.NextColor,
		g.Status,
		g.EndReason,
		g.WinnerColor,
		string(winLine),
		formatOptionalTime(g.AgentJoinedAt),
		formatOptionalTime(g.AgentLastSeenAt),
		boolInt(g.AgentThinking),
		formatOptionalTime(g.AgentThinkingSince),
		formatOptionalTime(g.AgentBlackJoinedAt),
		formatOptionalTime(g.AgentBlackLastSeenAt),
		boolInt(g.AgentBlackThinking),
		formatOptionalTime(g.AgentBlackThinkingSince),
		formatOptionalTime(g.AgentWhiteJoinedAt),
		formatOptionalTime(g.AgentWhiteLastSeenAt),
		boolInt(g.AgentWhiteThinking),
		formatOptionalTime(g.AgentWhiteThinkingSince),
		formatTime(g.UpdatedAt),
		g.ID,
	)
	return err
}

type gameScanner interface {
	Scan(dest ...any) error
}

func scanGame(scanner gameScanner) (game.Game, error) {
	var g game.Game
	var forbiddenInt int
	var agentStrategy string
	var ownerID string
	var winLineJSON string
	var agentJoinedAt string
	var agentLastSeenAt string
	var agentThinking int
	var agentThinkingSince string
	var agentBlackJoinedAt string
	var agentBlackLastSeenAt string
	var agentBlackThinking int
	var agentBlackThinkingSince string
	var agentWhiteJoinedAt string
	var agentWhiteLastSeenAt string
	var agentWhiteThinking int
	var agentWhiteThinkingSince string
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&g.ID,
		&g.Mode,
		&g.HumanToken,
		&g.AgentToken,
		&g.AgentBlackToken,
		&g.AgentWhiteToken,
		&g.HumanColor,
		&g.AgentColor,
		&forbiddenInt,
		&agentStrategy,
		&ownerID,
		&g.NextColor,
		&g.Status,
		&g.EndReason,
		&g.WinnerColor,
		&winLineJSON,
		&agentJoinedAt,
		&agentLastSeenAt,
		&agentThinking,
		&agentThinkingSince,
		&agentBlackJoinedAt,
		&agentBlackLastSeenAt,
		&agentBlackThinking,
		&agentBlackThinkingSince,
		&agentWhiteJoinedAt,
		&agentWhiteLastSeenAt,
		&agentWhiteThinking,
		&agentWhiteThinkingSince,
		&createdAt,
		&updatedAt,
		&g.MoveCount,
	); err != nil {
		return game.Game{}, err
	}
	if winLineJSON == "" {
		winLineJSON = "[]"
	}
	if g.Mode == "" {
		g.Mode = game.ModeHumanAgent
	}
	g.Forbidden = forbiddenInt != 0
	g.AgentStrategy = game.NormalizeStrategy(agentStrategy)
	g.OwnerID = ownerID
	if err := json.Unmarshal([]byte(winLineJSON), &g.WinLine); err != nil {
		return game.Game{}, fmt.Errorf("decode win line for game %s: %w", g.ID, err)
	}
	g.AgentJoinedAt = parseOptionalTime(agentJoinedAt)
	g.AgentLastSeenAt = parseOptionalTime(agentLastSeenAt)
	g.AgentThinking = agentThinking != 0
	g.AgentThinkingSince = parseOptionalTime(agentThinkingSince)
	g.AgentBlackJoinedAt = parseOptionalTime(agentBlackJoinedAt)
	g.AgentBlackLastSeenAt = parseOptionalTime(agentBlackLastSeenAt)
	g.AgentBlackThinking = agentBlackThinking != 0
	g.AgentBlackThinkingSince = parseOptionalTime(agentBlackThinkingSince)
	g.AgentWhiteJoinedAt = parseOptionalTime(agentWhiteJoinedAt)
	g.AgentWhiteLastSeenAt = parseOptionalTime(agentWhiteLastSeenAt)
	g.AgentWhiteThinking = agentWhiteThinking != 0
	g.AgentWhiteThinkingSince = parseOptionalTime(agentWhiteThinkingSince)
	g.CreatedAt = parseTime(createdAt)
	g.UpdatedAt = parseTime(updatedAt)
	return g, nil
}

func playerFromToken(g game.Game, token string) (game.Player, error) {
	switch token {
	case g.HumanToken:
		if g.HumanToken == "" {
			return "", ErrUnauthorized
		}
		return game.Human, nil
	case g.AgentToken:
		if g.AgentToken == "" {
			return "", ErrUnauthorized
		}
		return game.Agent, nil
	case g.AgentBlackToken:
		if g.AgentBlackToken == "" {
			return "", ErrUnauthorized
		}
		return game.AgentBlack, nil
	case g.AgentWhiteToken:
		if g.AgentWhiteToken == "" {
			return "", ErrUnauthorized
		}
		return game.AgentWhite, nil
	default:
		return "", ErrUnauthorized
	}
}

func markAgentJoined(g *game.Game, player game.Player, now time.Time) {
	markAgentThinking(g, player, now, false)
}

func markAgentThinking(g *game.Game, player game.Player, now time.Time, thinking bool) {
	switch player {
	case game.Agent:
		if g.AgentJoinedAt == nil {
			g.AgentJoinedAt = &now
		}
		g.AgentLastSeenAt = &now
		g.AgentThinking = thinking
		if thinking {
			if g.AgentThinkingSince == nil {
				g.AgentThinkingSince = &now
			}
		} else {
			g.AgentThinkingSince = nil
		}
	case game.AgentBlack:
		if g.AgentBlackJoinedAt == nil {
			g.AgentBlackJoinedAt = &now
		}
		g.AgentBlackLastSeenAt = &now
		g.AgentBlackThinking = thinking
		if thinking {
			if g.AgentBlackThinkingSince == nil {
				g.AgentBlackThinkingSince = &now
			}
		} else {
			g.AgentBlackThinkingSince = nil
		}
	case game.AgentWhite:
		if g.AgentWhiteJoinedAt == nil {
			g.AgentWhiteJoinedAt = &now
		}
		g.AgentWhiteLastSeenAt = &now
		g.AgentWhiteThinking = thinking
		if thinking {
			if g.AgentWhiteThinkingSince == nil {
				g.AgentWhiteThinkingSince = &now
			}
		} else {
			g.AgentWhiteThinkingSince = nil
		}
	}
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return formatTime(*value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func parseOptionalTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed := parseTime(value)
	if parsed.IsZero() {
		return nil
	}
	return &parsed
}
