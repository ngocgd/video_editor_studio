// Command api runs the Loomtale HTTP API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"loomtale/api/internal/dbpool"
	"loomtale/api/internal/health"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/obs"
	"loomtale/api/internal/storage"
)

func main() {
	// The distroless runtime image has no shell, so the compose healthcheck
	// execs this same binary instead of curl.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}

	if err := run(); err != nil {
		slog.Error("api exited with error", "error", err)
		os.Exit(1)
	}
}

func runHealthcheck() int {
	addr := envOr("API_ADDR", ":8080")
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1" + addr + "/api/v1/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run() error {
	var cfg config
	if err := env.Parse(&cfg); err != nil {
		return err
	}

	logger := obs.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := dbpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var storageCfg storage.Config
	if err := env.Parse(&storageCfg); err != nil {
		return err
	}
	objectStore, err := storage.New(storageCfg)
	if err != nil {
		return err
	}

	h := &health.Handler{Version: cfg.Version, DB: pool, Storage: objectStore}
	strict := gen.NewStrictHandler(h, nil)

	r := chi.NewRouter()
	r.Use(httpx.RequestIDMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Route("/api/v1", func(api chi.Router) {
		gen.HandlerFromMux(strict, api)
	})

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining", "timeout", cfg.ShutdownTimeout)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}
