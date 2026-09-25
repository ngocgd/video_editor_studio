// Command worker runs the Loomtale render/job worker process. Phase 1 ships
// a minimal process with a liveness endpoint and graceful shutdown so the
// compose stack and CI are green end to end; phase 3 adds River-backed job
// orchestration. The distroless runtime image has no shell, so the compose
// healthcheck runs this same binary with a "healthcheck" argument instead
// of curl.
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

	"loomtale/api/internal/obs"
)

const healthAddr = "127.0.0.1:8081"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}

	logger := obs.NewLogger(envOr("API_LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	server := &http.Server{Addr: healthAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	slog.Info("worker started; awaiting job orchestration (phase 3)")
	select {
	case err := <-errCh:
		if err != nil {
			slog.Error("worker health endpoint failed", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		slog.Info("worker received shutdown signal, exiting")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}
}

func runHealthcheck() int {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + healthAddr + "/healthz")
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
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
