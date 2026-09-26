// Command worker runs the Loomtale job worker process: a River client
// that claims and executes pipeline steps (see api/internal/pipeline),
// plus the reconciler that resumes work after a crash. Phases 6-10
// register their StepHandlers into the same Registry this process builds
// before Start; this phase ships no real handlers, so a fresh stack has
// nothing to claim yet and idles cleanly. The distroless runtime image
// has no shell, so the compose healthcheck runs this same binary with a
// "healthcheck" argument instead of curl.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"loomtale/api/internal/analytics"
	"loomtale/api/internal/crypto/envelope"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/dbpool"
	"loomtale/api/internal/models"
	"loomtale/api/internal/obs"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/bootstrap"
	"loomtale/api/internal/providers/llmcheck"
	"loomtale/api/internal/providers/workerstatus"
	"loomtale/api/internal/secrets"
	"loomtale/api/internal/storage"
	"loomtale/api/internal/story"
)

const healthAddr = "127.0.0.1:8081"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	if err := run(); err != nil {
		slog.Error("worker exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var cfg config
	if err := env.Parse(&cfg); err != nil {
		return err
	}
	logger := obs.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	if err := pipeline.AssertTimeoutsBelowRescue(); err != nil {
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
	logSink := &pipeline.AssetLogSink{Queries: queries, Storage: internalStore}

	// The envelope-encryption KEK must be the same one cmd/api mounts
	// (MASTER_KEY_PATH), since this worker opens the same tenants' BYOK
	// secrets that cmd/api's Settings > LLM page writes.
	kek, keyID, err := envelope.LoadKEK(cfg.MasterKeyPath)
	if err != nil {
		return err
	}
	sealer, err := envelope.NewSealer(keyID, kek)
	if err != nil {
		return err
	}
	secretsStore := &secrets.Store{Sealer: sealer, Queries: queries}

	llmRegistry, _, err := bootstrap.Build(bootstrap.Config{
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

	registry := pipeline.NewRegistry()
	for _, h := range story.Handlers(llmRegistry, queries) {
		registry.Register(h)
	}
	for _, h := range llmcheck.Handlers(llmRegistry) {
		registry.Register(h)
	}
	registry.Register(&analytics.ExplainHandler{Registry: llmRegistry})

	manifest, err := models.Embedded()
	if err != nil {
		return err
	}
	modelStore := &models.Store{Queries: queries}
	loadGate := &models.LoadGate{Manifest: manifest, Store: modelStore, Dir: cfg.ModelsDir}
	residency, probe, residencyManager, err := buildResidency(ctx, cfg, manifest, loadGate.Check)
	if err != nil {
		return err
	}
	// models.* steps only run on a GPU worker: that is the one with the
	// models volume (read-write, for pulls) and the GPU network (for
	// load/unload). A worker without them registers nothing, so it never
	// claims a step it cannot run.
	modelSteps := cfg.WorkerGPU && cfg.ModelsDir != ""
	if modelSteps {
		downloader := &models.Downloader{Dir: cfg.ModelsDir, HostDiskDir: cfg.ModelsHostDiskDir, HTTP: downloadHTTPClient(), Files: modelStore}
		models.RegisterSteps(registry, manifest, modelStore, downloader, residency)
	}
	saasMode := strings.EqualFold(cfg.AppMode, "saas")
	startWorkerStatusHeartbeat(ctx, queries, probe, residencyManager, func(ctx context.Context) map[string]workerstatus.ProviderInfo {
		return llmProviderStatus(ctx, llmRegistry.Providers, saasMode)
	})

	// A worker must never enable a queue no registered handler actually
	// resolves to: claiming a step it cannot run destroys it (the CAS
	// claim is a one-way door). The llm.* handlers (see story.Handlers
	// above) only ever resolve to QueueLLM or, for the Ollama provider,
	// QueueGPU (see registry.Registry.QueueFor), and models.pull uses
	// QueueIO only where the models steps are registered — so cpu/render
	// (and io elsewhere) stay disabled here until a later phase registers a
	// handler that actually uses them; enabling them unconditionally
	// whenever registry.Len() > 0 (as before phase 6, when the registry
	// was always empty and this was moot) would let this process's cpu/
	// render/io queue slots steal and permanently snooze jobs meant for
	// another registry/handler entirely, including other processes and
	// tests sharing the same Postgres instance.
	var queueConfig map[string]river.QueueConfig
	if registry.Len() > 0 {
		queueConfig = map[string]river.QueueConfig{
			pipeline.QueueLLM: {MaxWorkers: cfg.LLMWorkers},
		}
		if cfg.WorkerGPU {
			queueConfig[pipeline.QueueGPU] = river.QueueConfig{MaxWorkers: 1}
		}
		if modelSteps {
			queueConfig[pipeline.QueueIO] = river.QueueConfig{MaxWorkers: cfg.IOWorkers}
		}
	}

	// Engine is built with River left nil, then patched in below: the
	// worker bundle (needed to construct the client) holds a StepWorker
	// that references this same Engine by pointer, so the dependency
	// only resolves one way at a time.
	engine := pipeline.NewEngine(pool.Pool, queries, nil, registry, nil, nil)
	gpuExecutor := pipeline.NewGPUExecutor(engine, residency, cfg.RenderReserveMB)

	// Channel analytics sync runs as plain River jobs on its own queue
	// (not pipeline steps), enabled only when a Google OAuth client is
	// configured; the leader also schedules the daily sync of every
	// channel.
	var periodicJobs []*river.PeriodicJob
	workers := river.NewWorkers()
	syncer, err := analyticsSyncer(cfg, pool.Pool, queries, secretsStore)
	if err != nil {
		return err
	}
	if syncer != nil {
		if queueConfig == nil {
			queueConfig = map[string]river.QueueConfig{}
		}
		queueConfig[analytics.QueueAnalytics] = river.QueueConfig{MaxWorkers: cfg.AnalyticsWorkers}
		analytics.AddWorkers(workers, syncer)
		periodicJobs = analytics.PeriodicJobs()
	}
	river.AddWorker(workers, &pipeline.StepWorker{
		Engine:          engine,
		GPU:             gpuExecutor,
		RenderReserveMB: cfg.RenderReserveMB,
		Sink:            logSink,
	})

	riverClient, err := river.NewClient(riverpgxv5.New(pool.Pool), &river.Config{
		Queues:               queueConfig,
		Workers:              workers,
		PeriodicJobs:         periodicJobs,
		RescueStuckJobsAfter: pipeline.RescueStuckJobsAfter,
	})
	if err != nil {
		return err
	}
	engine.River = riverClient

	reconciler := &pipeline.Reconciler{Engine: engine}
	reconcilerCtx, stopReconciler := context.WithCancel(context.Background())
	defer stopReconciler()
	go reconciler.Run(reconcilerCtx)

	// river.Client.Start requires at least one configured queue: with an
	// empty registry (nothing to work yet), this client exists only to
	// let the reconciler insert jobs via InsertTx, exactly like the
	// insert-only client cmd/api builds. Started is tracked so shutdown
	// below only calls Stop on a client that was actually started.
	started := len(queueConfig) > 0
	if started {
		if err := riverClient.Start(ctx); err != nil {
			return err
		}
	}
	slog.Info("worker started", "gpu_enabled", cfg.WorkerGPU, "cpu_workers", cfg.CPUWorkers, "registered_kinds", registry.Len(), "queues_enabled", len(queueConfig))

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	healthServer := &http.Server{Addr: healthAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	healthErrCh := make(chan error, 1)
	go func() {
		if err := healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			healthErrCh <- err
		}
		close(healthErrCh)
	}()

	select {
	case err := <-healthErrCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		slog.Info("worker received shutdown signal, draining", "timeout", cfg.ShutdownTimeout)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = healthServer.Shutdown(shutdownCtx)

	stopReconciler()

	if !started {
		return nil
	}
	// Soft stop: let running jobs finish, up to ShutdownTimeout, then
	// escalate to a hard stop that cancels every job's context.
	softCtx, cancelSoft := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelSoft()
	if err := riverClient.Stop(softCtx); err != nil {
		slog.Warn("soft stop did not finish in time, cancelling running jobs", "error", err)
		hardCtx, cancelHard := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelHard()
		if err := riverClient.StopAndCancel(hardCtx); err != nil {
			return err
		}
	}
	return nil
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
