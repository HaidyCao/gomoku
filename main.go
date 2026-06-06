package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"wuziqi/internal/server"
	"wuziqi/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()})))

	addrFlag := flag.String("addr", "", "listen address, for example :8080 or 127.0.0.1:8090")
	portFlag := flag.String("port", "", "listen port, for example 8090")
	dbPathFlag := flag.String("db", "", "SQLite database path")
	staticDirFlag := flag.String("static", "", "frontend build directory")
	flag.Parse()

	addr := resolveAddr(*addrFlag, *portFlag)
	dbPath := firstNonEmpty(*dbPathFlag, env("DB_PATH", "data/wuziqi.db"))
	staticDir := firstNonEmpty(*staticDirFlag, env("STATIC_DIR", "frontend/dist"))

	rateLimitEnabled := envBool("RATE_LIMIT_ENABLED", true)
	serverConfig := server.Config{
		AllowedOrigins:    splitCSV(os.Getenv("ALLOWED_ORIGINS")),
		MaxJSONBodyBytes:  envInt64("MAX_JSON_BODY_BYTES", 16*1024),
		TrustProxyHeaders: envBool("TRUST_PROXY_HEADERS", false),
		RateLimit: server.RateLimitConfig{
			Enabled:           rateLimitEnabled,
			RequestsPerWindow: envInt("RATE_LIMIT_REQUESTS", 300),
			Window:            time.Duration(envInt("RATE_LIMIT_WINDOW_SECONDS", 60)) * time.Second,
		},
		CreateRateLimit: server.RateLimitConfig{
			Enabled:           rateLimitEnabled,
			RequestsPerWindow: envInt("CREATE_RATE_LIMIT_REQUESTS", 10),
			Window:            time.Duration(envInt("CREATE_RATE_LIMIT_WINDOW_SECONDS", 60)) * time.Second,
		},
	}

	// Root context cancelled on SIGINT/SIGTERM so an orchestrator restart or
	// `docker stop` triggers a graceful drain instead of an abrupt kill.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	openCtx, cancelOpen := context.WithTimeout(ctx, 10*time.Second)
	sqliteStore, err := store.Open(openCtx, dbPath)
	cancelOpen()
	if err != nil {
		slog.Error("open store", "error", err)
		os.Exit(1)
	}

	app := server.NewWithConfig(sqliteStore, staticDir, serverConfig)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Background retention bounds database growth by purging stale games.
	var retentionWG sync.WaitGroup
	if envBool("RETENTION_ENABLED", true) {
		retentionWG.Add(1)
		go func() {
			defer retentionWG.Done()
			runRetention(ctx, sqliteStore,
				time.Duration(envInt("RETENTION_INTERVAL_MINUTES", 30))*time.Minute,
				time.Duration(envInt("RETENTION_FINISHED_HOURS", 168))*time.Hour,
				time.Duration(envInt("RETENTION_ABANDONED_HOURS", 24))*time.Hour,
			)
		}()
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("wuziqi server listening", "url", displayURL(addr))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		slog.Error("http server failed", "error", err)
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining in-flight requests")
	}
	// Cancel the root context (stops retention) and restore default signal
	// handling so a second Ctrl-C/SIGTERM force-kills if a drain hangs.
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}
	retentionWG.Wait()
	if err := sqliteStore.Close(); err != nil {
		slog.Error("close store", "error", err)
	}
	slog.Info("shutdown complete")
}

// runRetention purges stale games on an interval until ctx is cancelled.
func runRetention(ctx context.Context, st *store.SQLiteStore, interval, finishedOlderThan, abandonedOlderThan time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purgeCtx, cancel := context.WithTimeout(ctx, time.Minute)
			deleted, err := st.PurgeStaleGames(purgeCtx, finishedOlderThan, abandonedOlderThan)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Warn("retention purge failed", "error", err)
				continue
			}
			if deleted > 0 {
				slog.Info("retention purge", "deleted", deleted)
			}
		}
	}
}

func logLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func resolveAddr(addrFlag string, portFlag string) string {
	if addrFlag != "" {
		return addrFlag
	}
	if portFlag != "" {
		return portAddr(portFlag)
	}
	if addr := os.Getenv("ADDR"); addr != "" {
		return addr
	}
	if port := os.Getenv("PORT"); port != "" {
		return portAddr(port)
	}
	return "127.0.0.1:8080"
}

func portAddr(port string) string {
	return "127.0.0.1:" + strings.TrimPrefix(strings.TrimSpace(port), ":")
}

func displayURL(addr string) string {
	switch {
	case strings.HasPrefix(addr, ":"):
		return "http://localhost" + addr
	case strings.HasPrefix(addr, "0.0.0.0:"):
		return "http://localhost:" + strings.TrimPrefix(addr, "0.0.0.0:")
	case strings.HasPrefix(addr, "[::]:"):
		return "http://localhost:" + strings.TrimPrefix(addr, "[::]:")
	default:
		return "http://" + addr
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func env(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		slog.Warn("invalid integer env var, using fallback", "key", key, "value", value, "fallback", fallback)
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		slog.Warn("invalid integer env var, using fallback", "key", key, "value", value, "fallback", fallback)
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		slog.Warn("invalid boolean env var, using fallback", "key", key, "value", value, "fallback", fallback)
		return fallback
	}
	return parsed
}
