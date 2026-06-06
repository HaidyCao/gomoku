package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"wuziqi/internal/store"
)

func TestResolveAddrDefaultsToLocalhost(t *testing.T) {
	t.Setenv("ADDR", "")
	t.Setenv("PORT", "")

	if got := resolveAddr("", ""); got != "127.0.0.1:8080" {
		t.Fatalf("default addr = %q", got)
	}
	if got := resolveAddr("", "8090"); got != "127.0.0.1:8090" {
		t.Fatalf("port addr = %q", got)
	}
	if got := resolveAddr("0.0.0.0:8080", "8090"); got != "0.0.0.0:8080" {
		t.Fatalf("explicit addr = %q", got)
	}
}

// TestRunRetentionStopsOnContextCancel guards the graceful-shutdown contract:
// the retention goroutine must exit when the root context is cancelled so that
// retentionWG.Wait() in main() can return and the store can close cleanly.
func TestRunRetentionStopsOnContextCancel(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "wuziqi.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		// Long interval so the loop only exits via context cancellation.
		runRetention(ctx, st, time.Hour, time.Hour, time.Hour)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runRetention did not stop after context cancel")
	}
}
