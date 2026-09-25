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

	"loomtale/api/internal/assetsapi"
	"loomtale/api/internal/auditapi"
	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/authapi"
	"loomtale/api/internal/crypto/envelope"
	"loomtale/api/internal/csrf"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/dbpool"
	"loomtale/api/internal/health"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/obs"
	"loomtale/api/internal/ops"
	"loomtale/api/internal/ratelimit"
	"loomtale/api/internal/rbac"
	"loomtale/api/internal/secheaders"
	"loomtale/api/internal/storage"
	"loomtale/api/internal/validation"
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

func run() error {
	var cfg config
	if err := env.Parse(&cfg); err != nil {
		return err
	}

	logger := obs.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	// Fail fast if the envelope-encryption key is missing or malformed:
	// the process must never start in a state where secrets could
	// silently go unencrypted. The Sealer itself is not wired to a
	// handler in this phase (no route creates a secret yet); later
	// phases that add OAuth/API-key storage build one the same way.
	kek, keyID, err := envelope.LoadKEK(cfg.MasterKeyPath)
	if err != nil {
		return err
	}
	if _, err := envelope.NewSealer(keyID, kek); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := dbpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	queries := dbgen.New(pool)

	var storageCfg storage.Config
	if err := env.Parse(&storageCfg); err != nil {
		return err
	}
	internalStore, err := storage.New(storageCfg)
	if err != nil {
		return err
	}

	var browserCfg storage.BrowserConfig
	if err := env.Parse(&browserCfg); err != nil {
		return err
	}
	browserStore, err := storage.NewBrowser(browserCfg)
	if err != nil {
		return err
	}

	spec, err := gen.GetSwagger()
	if err != nil {
		return err
	}
	minRoles, err := rbac.BuildMinRoles(spec)
	if err != nil {
		return err
	}
	requestValidator, err := validation.Middleware(spec)
	if err != nil {
		return err
	}

	allowedOrigins := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		if o != "" {
			allowedOrigins[o] = true
		}
	}

	srv := &server{
		Handler: &health.Handler{
			Version: cfg.Version,
			DB:      pool,
			Storage: internalStore,
			Backup:  &ops.BackupStatus{Queries: queries},
		},
		AuthAPI: &authapi.AuthAPI{
			Pool:         pool,
			Queries:      queries,
			Store:        authpkg.Store{},
			LoginPerUser: ratelimit.NewDBBucket(queries, 5, 5.0/60),
			LoginPerIP:   ratelimit.NewDBBucket(queries, 20, 20.0/3600),
		},
		AssetsAPI: &assetsapi.AssetsAPI{
			Pool:     pool,
			Queries:  queries,
			Internal: internalStore,
			Browser:  browserStore,
		},
		AuditAPI: &auditapi.AuditAPI{Queries: queries},
	}

	strict := gen.NewStrictHandlerWithOptions(srv, []gen.StrictMiddlewareFunc{rbac.Middleware(minRoles)}, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  problemErrorHandler(http.StatusBadRequest, "invalid request"),
		ResponseErrorHandlerFunc: problemErrorHandler(http.StatusInternalServerError, "internal error"),
	})

	generalLimiter := ratelimit.NewMemory(100, 100.0/60)
	headers := secheaders.Config{MediaOrigin: cfg.MediaOrigin, PublicURL: cfg.PublicURL}
	sessionMW := authpkg.Middleware(authpkg.Store{}, queries)
	csrfMW := csrf.Middleware(authpkg.CSRFLookup, allowedOrigins, csrfRejected)

	r := chi.NewRouter()
	r.Use(httpx.RequestIDMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(httpx.RealIP(cfg.TrustedProxyCIDRs))
	r.Use(headers.Middleware)
	r.Use(generalLimiter.Middleware(httpx.ClientIP, tooManyRequests))
	r.Use(httpx.WithRequestMiddleware)
	r.Use(middleware.Logger)
	r.Use(sessionMW)
	r.Use(csrfMW)
	r.Use(requestValidator)
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

func tooManyRequests(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteProblem(w, httpx.Problem{Title: "rate limited", Status: http.StatusTooManyRequests})
}

func csrfRejected(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteProblem(w, httpx.Problem{Title: "CSRF check failed", Status: http.StatusForbidden})
}

// problemErrorHandler maps a handler/binding error to problem+json,
// logging the real error server-side (never in the client-facing title,
// which stays a fixed, non-leaking string) so an operator can diagnose it
// from logs without exposing internals to the caller.
func problemErrorHandler(status int, title string) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		slog.ErrorContext(r.Context(), "request failed", "error", err, "path", r.URL.Path, "request_id", httpx.RequestID(r.Context()))
		httpx.WriteProblem(w, httpx.Problem{Title: title, Status: status})
	}
}
