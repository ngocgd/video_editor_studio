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
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

	if cfg.HostFallback {
		if err := requireLoopbackBind(cfg.Addr); err != nil {
			return fmt.Errorf("llmcli: LLMCLI_HOST_FALLBACK requires a loopback-only bind address: %w", err)
		}
		slog.Warn("llmcli running in host-fallback mode: using the host's own claude login, never reading its credential file directly", "addr", cfg.Addr)
	}

	// In host-fallback mode CLAUDE_OAUTH_TOKEN_PATH is deliberately
	// unset: the spawned claude process resolves auth from the host
	// user's own existing login, and this process never opens that
	// credential file itself.
	var oauthToken string
	var err error
	if !cfg.HostFallback {
		oauthToken, err = readSecretFile(cfg.OAuthTokenPath)
		if err != nil {
			return err
		}
	}
	bearerToken, err := readSecretFile(cfg.BearerTokenPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	version, checkErr := selfCheck(ctx, cfg.Binary, cfg.PinnedVersion)
	disabledReason := ""
	switch {
	case checkErr != nil:
		disabledReason = checkErr.Error()
		slog.Error("llmcli self-check failed; provider will report unhealthy", "error", checkErr)
	case cfg.HostFallback:
		// No token check here: auth comes from the host's own claude
		// login, which this process cannot observe in advance the way
		// it can check a mounted token file. A real /v1/run call will
		// surface an auth failure through the CLI's own error output if
		// the host session is not actually logged in.
		slog.Info("llmcli self-check passed (host-fallback mode)", "version", version)
	case oauthToken == "":
		// A missing/empty token means every real call would fail
		// authentication anyway; report that now instead of a healthy
		// status that turns into a confusing per-call failure. The
		// binary/flag self-check above still ran (and is still logged),
		// so an operator gets both signals distinctly.
		disabledReason = "no CLAUDE_CODE_OAUTH_TOKEN configured (run 'claude setup-token' and write the result to the mounted secret file)"
		slog.Warn("llmcli oauth token is empty; provider will report unhealthy", "version", version)
	default:
		slog.Info("llmcli self-check passed", "version", version)
	}

	rn := newRunner(cfg.Binary, cfg.WorkRoot, cfg.MaxConcurrent, cfg.Timeout)
	h := &handler{
		runner:         rn,
		version:        version,
		model:          cfg.Model,
		systemPrompt:   cfg.SystemPrompt,
		oauthToken:     oauthToken,
		bearerToken:    bearerToken,
		disabledReason: disabledReason,
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

// requireLoopbackBind refuses any bind address whose host part is not
// empty (":8090", which the OS resolves to all interfaces — not safe for
// host-fallback mode), 127.0.0.1 or localhost.
func requireLoopbackBind(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("address %q binds all interfaces; use 127.0.0.1:<port>", addr)
	}
	if host != "127.0.0.1" && !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("address %q is not loopback", addr)
		}
	}
	return nil
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
