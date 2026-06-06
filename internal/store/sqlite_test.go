package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"wuziqi/internal/game"
)

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "wuziqi.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// backdate forces a game's updated_at to age ago so retention can act on it.
func backdate(t *testing.T, s *SQLiteStore, id string, age time.Duration) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE games SET updated_at = ? WHERE id = ?`,
		formatTime(time.Now().UTC().Add(-age)), id); err != nil {
		t.Fatalf("backdate %s: %v", id, err)
	}
}

func moveCount(t *testing.T, s *SQLiteStore, id string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM moves WHERE game_id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("count moves for %s: %v", id, err)
	}
	return n
}

func TestPurgeStaleGames(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Old finished game with a move — should be purged together with its move.
	oldFinished, err := s.CreateGame(ctx, game.ModeHumanAgent, game.Black)
	if err != nil {
		t.Fatalf("create old finished: %v", err)
	}
	if _, err := s.ApplyMove(ctx, oldFinished.ID, oldFinished.HumanToken, 7, 7); err != nil {
		t.Fatalf("apply move: %v", err)
	}
	if _, err := s.Resign(ctx, oldFinished.ID, oldFinished.HumanToken); err != nil {
		t.Fatalf("resign old finished: %v", err)
	}
	if c := moveCount(t, s, oldFinished.ID); c != 1 {
		t.Fatalf("expected 1 move before purge, got %d", c)
	}
	backdate(t, s, oldFinished.ID, 200*time.Hour)

	// Recent finished game — should be kept.
	recentFinished, err := s.CreateGame(ctx, game.ModeHumanAgent, game.Black)
	if err != nil {
		t.Fatalf("create recent finished: %v", err)
	}
	if _, err := s.Resign(ctx, recentFinished.ID, recentFinished.HumanToken); err != nil {
		t.Fatalf("resign recent finished: %v", err)
	}

	// Old abandoned in-progress game — should be purged.
	oldActive, err := s.CreateGame(ctx, game.ModeHumanAgent, game.Black)
	if err != nil {
		t.Fatalf("create old active: %v", err)
	}
	backdate(t, s, oldActive.ID, 48*time.Hour)

	// Recent in-progress game — should be kept.
	recentActive, err := s.CreateGame(ctx, game.ModeHumanAgent, game.Black)
	if err != nil {
		t.Fatalf("create recent active: %v", err)
	}

	deleted, err := s.PurgeStaleGames(ctx, 168*time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}

	if _, err := s.GetGame(ctx, oldFinished.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old finished should be gone, got %v", err)
	}
	if c := moveCount(t, s, oldFinished.ID); c != 0 {
		t.Fatalf("old finished moves should cascade-delete, got %d", c)
	}
	if _, err := s.GetGame(ctx, oldActive.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old abandoned game should be gone, got %v", err)
	}
	if _, err := s.GetGame(ctx, recentFinished.ID); err != nil {
		t.Fatalf("recent finished game should remain: %v", err)
	}
	if _, err := s.GetGame(ctx, recentActive.ID); err != nil {
		t.Fatalf("recent active game should remain: %v", err)
	}
}

func TestPurgeStaleGamesDisabledCategories(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	old, err := s.CreateGame(ctx, game.ModeHumanAgent, game.Black)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Resign(ctx, old.ID, old.HumanToken); err != nil {
		t.Fatalf("resign: %v", err)
	}
	backdate(t, s, old.ID, 500*time.Hour)

	// Non-positive durations disable purging entirely.
	deleted, err := s.PurgeStaleGames(ctx, 0, 0)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0 when disabled", deleted)
	}
	if _, err := s.GetGame(ctx, old.ID); err != nil {
		t.Fatalf("game should remain when purge disabled: %v", err)
	}
}

func TestPingAfterClose(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "wuziqi.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("ping before close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("ping after close should return an error")
	}
}
