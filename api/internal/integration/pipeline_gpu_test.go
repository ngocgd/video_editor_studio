//go:build integration

package integration

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// fakeResidency is a minimal, real (not response-mocked) ModelResidency:
// Ensure/UnloadAll/Current genuinely track state, they just never touch a
// GPU.
type fakeResidency struct{ current atomic.Pointer[pipeline.ModelRef] }

func (r *fakeResidency) Ensure(_ context.Context, m pipeline.ModelRef) error {
	r.current.Store(&m)
	return nil
}
func (r *fakeResidency) UnloadAll(context.Context) error { r.current.Store(nil); return nil }
func (r *fakeResidency) Current() *pipeline.ModelRef      { return r.current.Load() }

// TestGpuAdvisoryLockSerializesTwoExecutors runs two GPUExecutor
// instances concurrently against the same Postgres database (standing in
// for two worker processes): each tries to run its own gpu step at the
// same time. The advisory lock (a real Postgres pg_try_advisory_lock, not
// a mock) must ensure their handlers never overlap.
func TestGpuAdvisoryLockSerializesTwoExecutors(t *testing.T) {
	skipIfAPIUnreachable(t)
	var inFlight atomic.Int32
	var overlapped atomic.Bool

	registry := pipeline.NewRegistry()
	registry.Register(&fakeHandler{kind: "gpu-exclusive", queue: pipeline.QueueGPU, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		if inFlight.Add(1) > 1 {
			overlapped.Store(true)
		}
		time.Sleep(300 * time.Millisecond)
		inFlight.Add(-1)
		return pipeline.Output{}, nil
	}})

	engineA, pool := pipelineEngine(t, registry)
	engineB, _ := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "gpu-lock-tenant")

	runID := idconv.NewV7()
	stepA := idconv.NewV7()
	stepB := idconv.NewV7()
	if _, err := engineA.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "gpu-lock-test",
		Steps: []pipeline.StepSpec{
			{ID: stepA, Kind: "gpu-exclusive", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
			{ID: stepB, Kind: "gpu-exclusive", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
		},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	gA := pipeline.NewGPUExecutor(engineA, &fakeResidency{}, 0)
	gB := pipeline.NewGPUExecutor(engineB, &fakeResidency{}, 0)

	// A real river.Client retries a JobSnoozeError after its delay; these
	// two executors are driven directly (no client loop behind them), so
	// the test plays that same role: whichever one loses the advisory
	// lock race snoozes and must be retried until the step is claimed.
	runUntilDone := func(g *pipeline.GPUExecutor, jobID int64, stepID uuid.UUID) error {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			err := g.Run(context.Background(), jobID, 1, pipeline.MaxTransientAttempts, []uuid.UUID{stepID}, nil)
			var snooze *river.JobSnoozeError
			if errors.As(err, &snooze) {
				continue
			}
			return err
		}
		return context.DeadlineExceeded
	}

	errCh := make(chan error, 2)
	go func() { errCh <- runUntilDone(gA, 7001, stepA) }()
	go func() { errCh <- runUntilDone(gB, 7002, stepB) }()

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("gpu executor run: %v", err)
		}
	}

	if overlapped.Load() {
		t.Fatal("expected the two GPU executors to never run their handlers concurrently")
	}
	if s := readStep(t, pool, stepA); s.Status != pipeline.StatusDone {
		t.Fatalf("expected step A done, got %q", s.Status)
	}
	if s := readStep(t, pool, stepB); s.Status != pipeline.StatusDone {
		t.Fatalf("expected step B done, got %q", s.Status)
	}
}

// TestGpuLockConnectionLossCancelsRunningJob kills the advisory-lock
// connection's own backend from a second connection mid-job and asserts
// the running handler observes context cancellation, matching the
// watchdog's contract: losing the lock connection must never let a job
// keep running unsupervised.
func TestGpuLockConnectionLossCancelsRunningJob(t *testing.T) {
	skipIfAPIUnreachable(t)
	cancelled := make(chan struct{}, 1)

	registry := pipeline.NewRegistry()
	registry.Register(&fakeHandler{kind: "gpu-watchdog", queue: pipeline.QueueGPU, run: func(ctx context.Context, _ *pipeline.StepContext) (pipeline.Output, error) {
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
			return nil, ctx.Err()
		case <-time.After(15 * time.Second):
			return pipeline.Output{}, nil
		}
	}})

	// A dedicated, uniquely-named connection pool for this engine: killing
	// its own advisory-lock connection below must never risk hitting an
	// unrelated backend (the live worker container's own connections, or
	// another test's).
	appName := "gpu-watchdog-test-" + uuid.NewString()
	engine, _ := pipelineEngineOnPool(t, registry, markedPool(t, appName))
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "gpu-watchdog-tenant")

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "gpu-watchdog-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "gpu-watchdog", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	g := pipeline.NewGPUExecutor(engine, &fakeResidency{}, 0)
	go func() { _ = g.Run(context.Background(), 7101, 1, pipeline.MaxTransientAttempts, []uuid.UUID{stepID}, nil) }()

	// Give the executor time to acquire the lock and start the handler,
	// then find and kill exactly its own backend (matched by
	// application_name, not "every advisory lock holder") from a separate
	// connection: the watchdog's next ping (every 5s in production; the
	// test can't shrink that constant, so it waits it out) must then
	// cancel the job.
	time.Sleep(500 * time.Millisecond)
	// pg_terminate_backend requires either superuser or the same role as
	// the target backend; markedPool connects as the app role (same DSN
	// as appPool), so the terminating connection must too.
	terminator := appPool(t)
	if _, err := terminator.Exec(context.Background(),
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = $1 AND pid <> pg_backend_pid()`, appName,
	); err != nil {
		t.Fatalf("terminate lock backend: %v", err)
	}

	select {
	case <-cancelled:
	case <-time.After(15 * time.Second):
		t.Fatal("expected the watchdog to cancel the running job within a couple of ping intervals of the lock connection dying")
	}
}
