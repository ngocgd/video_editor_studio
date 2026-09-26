// Command api runs the Loomtale HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

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
	"loomtale/api/internal/models"
	"loomtale/api/internal/modelsapi"
	"loomtale/api/internal/obs"
	"loomtale/api/internal/ops"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/pipelineapi"
	"loomtale/api/internal/providers/bootstrap"
	"loomtale/api/internal/providers/llmcheck"
	"loomtale/api/internal/providers/workerstatus"
	"loomtale/api/internal/quota"
	"loomtale/api/internal/ratelimit"
	"loomtale/api/internal/rbac"
	"loomtale/api/internal/secheaders"
	"loomtale/api/internal/secrets"
	"loomtale/api/internal/settingsapi"
	"loomtale/api/internal/sse"
	"loomtale/api/internal/storage"
	"loomtale/api/internal/story"
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
	sealer, err := envelope.NewSealer(keyID, kek)
	if err != nil {
		return err
	}
	// The CSRF pepper reuses the KEK bytes with domain separation (see
	// package csrf's doc comment), rather than a second mounted secret:
	// it is never used for anything AES-GCM would touch.
	csrfPepper := kek[:]

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

	if err := pipeline.AssertTimeoutsBelowRescue(); err != nil {
		return err
	}
	// Insert-only River client: the API process enqueues jobs but never
	// runs them (cmd/worker does), so it needs no Queues/Workers config.
	riverClient, err := river.NewClient(riverpgxv5.New(pool.Pool), &river.Config{
		RescueStuckJobsAfter: pipeline.RescueStuckJobsAfter,
	})
	if err != nil {
		return err
	}
	quotaChecker := &quota.Checker{}
	manifest, err := models.Embedded()
	if err != nil {
		return err
	}
	modelStore := &models.Store{Queries: queries}
	secretsStore := &secrets.Store{Sealer: sealer, Queries: queries}
	llmRegistry, llmStore, err := bootstrap.Build(bootstrap.Config{
		AppMode:               cfg.AppMode,
		AllowedProviderHosts:  cfg.AllowedProviderHosts,
		OllamaURL:             cfg.OllamaURL,
		OllamaModel:           cfg.OllamaModel,
		LLMCLIURL:             cfg.LLMCLIURL,
		LLMCLIBearerTokenPath: cfg.LLMCLIBearerTokenPath,
		AnthropicAPIKeyPath:   cfg.AnthropicAPIKeyPath,
		AnthropicModel:        cfg.AnthropicModel,
		GeminiAPIKeyPath:      cfg.GeminiAPIKeyPath,
		GeminiModel:           cfg.GeminiModel,
	}, queries, secretsStore)
	if err != nil {
		return err
	}
	stepRegistry := pipeline.NewRegistry()
	// The api process only enqueues models.* steps; their handlers are
	// registered here so Enqueue can resolve queue and model, and they
	// refuse to Run (only the worker has the models volume and the GPU).
	models.RegisterSteps(stepRegistry, manifest, modelStore, nil, nil)
	for _, handler := range story.Handlers(llmRegistry, queries) {
		stepRegistry.Register(handler)
	}
	for _, handler := range llmcheck.Handlers(llmRegistry) {
		stepRegistry.Register(handler)
	}
	engine := pipeline.NewEngine(pool.Pool, queries, riverClient, stepRegistry, []pipeline.AdmissionCheck{quotaChecker.Check}, nil)
	hub := sse.NewHub(pool.Pool)
	hubCtx, stopHub := context.WithCancel(context.Background())
	defer stopHub()
	go hub.Run(hubCtx)

	// Cancelled (via server.RegisterOnShutdown below) when the process
	// starts a graceful shutdown, so every open SSE stream ends promptly
	// instead of holding server.Shutdown's wait open until
	// ShutdownTimeout on every deploy.
	shutdownSignal, cancelShutdownSignal := context.WithCancel(context.Background())
	defer cancelShutdownSignal()

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
			Pool:            pool,
			Queries:         queries,
			Store:           authpkg.Store{},
			CSRFPepper:      csrfPepper,
			HashLimiter:     authpkg.NewHashLimiter(cfg.ArgonMaxConcurrency),
			LoginPerUser:    ratelimit.NewDBBucket(queries, 5, 5.0/60),
			LoginPerIP:      ratelimit.NewDBBucket(queries, 20, 20.0/3600),
			LoginPerAccount: ratelimit.NewDBBucket(queries, 10, 10.0/3600),
		},
		AssetsAPI: &assetsapi.AssetsAPI{
			Pool:     pool,
			Queries:  queries,
			Internal: internalStore,
			Browser:  browserStore,
		},
		AuditAPI: &auditapi.AuditAPI{Queries: queries},
		PipelineAPI: &pipelineapi.PipelineAPI{
			Engine:  engine,
			Storage: internalStore,
			Hub:     hub,
			// This process has no network path to gpu_net (only the
			// worker joins it, see deploy/compose.gpu.yml), so Probe and
			// Residency stay nil; GetGpuStatus falls back to
			// WorkerStatus, the worker's own heartbeat row.
			Probe:          nil,
			Residency:      nil,
			WorkerStatus:   &workerstatus.Store{Queries: queries},
			ShutdownSignal: shutdownSignal,
		},
		SettingsAPI: &settingsapi.SettingsAPI{
			Registry:      llmRegistry,
			Store:         llmStore,
			Queries:       queries,
			Secrets:       secretsStore,
			WorkerStatus:  &workerstatus.Store{Queries: queries},
			TestRateLimit: ratelimit.NewDBBucket(queries, 5, 5.0/60),
			SecretsWrite:  secretsStore,
			Probe:         &llmcheck.Runner{Engine: engine, Queries: queries},
		},
		StoryAPI: &story.StoryAPI{
			Pool:     pool,
			Queries:  queries,
			Engine:   engine,
			Registry: llmRegistry,
			Internal: internalStore,
		},
		ModelsAPI: &modelsapi.ModelsAPI{
			Manifest:     manifest,
			Store:        modelStore,
			Queries:      queries,
			Engine:       engine,
			WorkerStatus: &workerstatus.Store{Queries: queries},
		},
	}

	strict := gen.NewStrictHandlerWithOptions(srv, []gen.StrictMiddlewareFunc{rbac.Middleware(minRoles)}, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  problemErrorHandler(http.StatusBadRequest, "invalid request"),
		ResponseErrorHandlerFunc: problemErrorHandler(http.StatusInternalServerError, "internal error"),
	})

	if cfg.RateLimitPerMinute <= 0 {
		return fmt.Errorf("API_RATE_LIMIT_PER_MINUTE must be positive, got %d", cfg.RateLimitPerMinute)
	}
	generalLimiter := ratelimit.NewMemory(float64(cfg.RateLimitPerMinute), float64(cfg.RateLimitPerMinute)/60)
	headers := secheaders.Config{MediaOrigin: cfg.MediaOrigin, PublicURL: cfg.PublicURL}
	sessionMW := authpkg.Middleware(authpkg.Store{}, queries)
	csrfMW := csrf.Middleware(csrfPepper, authpkg.CSRFLookup, allowedOrigins, csrfRejected)

	r := chi.NewRouter()
	r.Use(httpx.RequestIDMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(httpx.RealIP(cfg.TrustedProxyCIDRs))
	r.Use(headers.Middleware)
	r.Use(httpx.MaxBodyMiddleware)
	r.Use(generalLimiter.Middleware(httpx.ClientIP, tooManyRequests))
	r.Use(httpx.WithRequestMiddleware)
	r.Use(httpx.AccessLogMiddleware)
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
	server.RegisterOnShutdown(cancelShutdownSignal)

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
