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
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/dbpool"
	"loomtale/api/internal/obs"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/storage"
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

	registry := pipeline.NewRegistry()
	// Phases 6-10 register their StepHandlers here in their own
	// cmd/worker wiring change once they exist; phase 3 ships the engine
	// with none.

	var residency pipeline.ModelResidency = pipeline.NoopResidency{}
	// Phase 4 replaces this with a real residency backend and only then
	// is WORKER_GPU allowed to be true (see compose.gpu.yml).

	// A worker with no registered handler for any kind must never fetch a
	// job at all: claiming a step it cannot run destroys it (the CAS
	// claim is a one-way door). This is the only thing standing between
	// today's empty registry (nothing past phase 3 has registered a
	// handler yet) and every gpu/cpu/llm/render/io queue getting worked
	// by a process that immediately fails everything with "no handler".
	var queueConfig map[string]river.QueueConfig
	if registry.Len() > 0 {
		queueConfig = map[string]river.QueueConfig{
			pipeline.QueueCPU:    {MaxWorkers: cfg.CPUWorkers},
			pipeline.QueueLLM:    {MaxWorkers: cfg.LLMWorkers},
			pipeline.QueueRender: {MaxWorkers: cfg.RenderWorkers},
			pipeline.QueueIO:     {MaxWorkers: cfg.IOWorkers},
		}
		if cfg.WorkerGPU {
			queueConfig[pipeline.QueueGPU] = river.QueueConfig{MaxWorkers: 1}
		}
	}

	// Engine is built with River left nil, then patched in below: the
	// worker bundle (needed to construct the client) holds a StepWorker
	// that references this same Engine by pointer, so the dependency
	// only resolves one way at a time.
	engine := pipeline.NewEngine(pool.Pool, queries, nil, registry, nil, nil)
	gpuExecutor := pipeline.NewGPUExecutor(engine, residency, cfg.RenderReserveMB)

	workers := river.NewWorkers()
	river.AddWorker(workers, &pipeline.StepWorker{
		Engine:          engine,
		GPU:             gpuExecutor,
		RenderReserveMB: cfg.RenderReserveMB,
		Sink:            logSink,
	})

	riverClient, err := river.NewClient(riverpgxv5.New(pool.Pool), &river.Config{
		Queues:               queueConfig,
		Workers:              workers,
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
