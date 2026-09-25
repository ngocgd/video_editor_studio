// Command llmcli is the isolated sidecar that spawns the pinned `claude`
// CLI binary and relays its stream-json output as NDJSON over a
// bearer-token-authenticated internal HTTP endpoint. It holds only the
// Anthropic OAuth token (no DB, MinIO, KEK or YouTube secret); see
// deploy/docker/llmcli.Dockerfile and compose.yml's llm-cli service for
// the container-level isolation (read-only rootfs, non-root, tmpfs HOME,
// egress-proxy-only network).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	if err := run(); err != nil {
		slog.Error("llmcli exited with error", "error", err)
		os.Exit(1)
	}
}

// runHealthcheck matches the api/worker convention (the compose
// healthcheck execs this same binary instead of curl/wget, which the
// node base image may not have).
func runHealthcheck() int {
	addr := os.Getenv("LLMCLI_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1" + addr + "/healthz")
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func run() error {
	var cfg config
	if err := env.Parse(&cfg); err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	oauthToken, err := readSecretFile(cfg.OAuthTokenPath)
	if err != nil {
		return err
	}
	bearerToken, err := readSecretFile(cfg.BearerTokenPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	version, checkErr := selfCheck(ctx, cfg.Binary, cfg.PinnedVersion)
	disabledReason := ""
	if checkErr != nil {
		disabledReason = checkErr.Error()
		slog.Error("llmcli self-check failed; provider will report unhealthy", "error", checkErr)
	} else {
		slog.Info("llmcli self-check passed", "version", version)
	}

	rn := newRunner(cfg.Binary, cfg.WorkRoot, cfg.MaxConcurrent, cfg.Timeout)
	h := &handler{
		runner:          rn,
		model:           cfg.Model,
		systemPrompt:    cfg.SystemPrompt,
		oauthToken:      oauthToken,
		bearerToken:     bearerToken,
		disabledReason:  disabledReason,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.handleHealthz)
	mux.HandleFunc("/v1/run", h.handleRun)

	server := &http.Server{Addr: cfg.Addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	slog.Info("llmcli started", "addr", cfg.Addr, "model", cfg.Model, "disabled", disabledReason != "")

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		slog.Info("llmcli received shutdown signal")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

func readSecretFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s, nil
}

// writeJSONLine writes one NDJSON line and flushes it immediately so the
// caller sees streamed deltas as they arrive rather than buffered.
func writeJSONLine(w http.ResponseWriter, flusher http.Flusher, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
	if flusher != nil {
		flusher.Flush()
	}
}
