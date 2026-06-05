package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"wuziqi/internal/server"
	"wuziqi/internal/store"
)

func main() {
	addrFlag := flag.String("addr", "", "listen address, for example :8080 or 127.0.0.1:8090")
	portFlag := flag.String("port", "", "listen port, for example 8090")
	dbPathFlag := flag.String("db", "", "SQLite database path")
	staticDirFlag := flag.String("static", "", "frontend build directory")
	flag.Parse()

	addr := resolveAddr(*addrFlag, *portFlag)
	dbPath := firstNonEmpty(*dbPathFlag, env("DB_PATH", "data/wuziqi.db"))
	staticDir := firstNonEmpty(*staticDirFlag, env("STATIC_DIR", "frontend/dist"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sqliteStore, err := store.Open(ctx, dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer sqliteStore.Close()

	app := server.New(sqliteStore, staticDir)
	log.Printf("wuziqi server listening on %s", displayURL(addr))
	if err := http.ListenAndServe(addr, app.Handler()); err != nil {
		log.Fatal(err)
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
	return ":8080"
}

func portAddr(port string) string {
	return ":" + strings.TrimPrefix(strings.TrimSpace(port), ":")
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
