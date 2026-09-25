//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// chunkStepDuration is the test-only per-step duration used to size one
// batch chunk, standing in for the spec's "test-only 2s-chunk handler";
// kept at 1s here purely to keep this test's wall-clock cost low while
// still exercising real River scheduling end to end.
const chunkStepDuration = 1 * time.Second

// TestInteractiveStepWaitsAtMostOneChunkBehindBatch runs a real
// river.Client (Start/Stop, not a manual Dispatch call) against a
// single-worker cpu queue: a two-step batch chunk occupies the only
// worker slot, then an interactive-priority step is enqueued. It must
// start within one chunk's duration, not be starved behind further batch
// work.
func TestInteractiveStepWaitsAtMostOneChunkBehindBatch(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := appPool(t)
	queries := dbgen.New(pool)

	registry := pipeline.NewRegistry()
	registry.Register(&fakeHandler{kind: "sched-batch", queue: pipeline.QueueCPU, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		time.Sleep(chunkStepDuration)
		return pipeline.Output{}, nil
	}})
	interactiveStarted := make(chan time.Time, 1)
	registry.Register(&fakeHandler{kind: "sched-interactive", queue: pipeline.QueueCPU, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		interactiveStarted <- time.Now()
		return pipeline.Output{}, nil
	}})
	// A generous per-step estimate keeps both batch steps in a single
	// chunk (ChunkSize(1s) = 10, well above 2), so the interactive step
	// truly waits behind one whole chunk rather than a re-split one.
	estimator := func(kind string) time.Duration {
		if kind == "sched-batch" {
			return chunkStepDuration
		}
		return pipeline.DefaultStepEstimate(kind)
	}

	// Engine is built with River left nil, then patched in below: the
	// worker bundle (needed to construct the client) holds a StepWorker
	// that references this same Engine by pointer.
	engine := pipeline.NewEngine(pool, queries, nil, registry, nil, estimator)
	workers := river.NewWorkers()
	river.AddWorker(workers, &pipeline.StepWorker{Engine: engine})

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:               map[string]river.QueueConfig{pipeline.QueueCPU: {MaxWorkers: 1}},
		Workers:              workers,
		RescueStuckJobsAfter: pipeline.RescueStuckJobsAfter,
	})
	if err != nil {
		t.Fatalf("build river client: %v", err)
	}
	engine.River = riverClient

	// This is the only test in the package that starts a real,
	// polling river.Client: clear out any not-yet-worked job left behind
	// by an earlier test's Enqueue call (which creates a real river_job
	// row even though that test then drove completion manually through
	// Dispatch, never through an actual River fetch loop), so this
	// client's single cpu worker slot is not contended by unrelated
	// leftover jobs it has no handler for.
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM river_job WHERE queue = $1 AND state IN ('available', 'scheduled', 'retryable')`, pipeline.QueueCPU,
	); err != nil {
		t.Fatalf("clear leftover cpu jobs: %v", err)
	}

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()
	if err := riverClient.Start(ctx); err != nil {
		t.Fatalf("start river client: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = riverClient.Stop(stopCtx)
	}()

	owner := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, owner, "scheduling-tenant")

	runID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "sched-test",
		Steps: []pipeline.StepSpec{
			{ID: idconv.NewV7(), Kind: "sched-batch", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
			{ID: idconv.NewV7(), Kind: "sched-batch", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
		},
	}); err != nil {
		t.Fatalf("enqueue batch: %v", err)
	}

	// Give the single worker time to pick up and start the batch chunk
	// before the interactive step is even enqueued, so it genuinely has
	// to wait behind live work rather than racing an idle worker.
	time.Sleep(300 * time.Millisecond)

	enqueuedAt := time.Now()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: idconv.NewV7(), ScopeKind: "test", ScopeID: runID, Kind: "sched-test",
		Steps: []pipeline.StepSpec{{ID: idconv.NewV7(), Kind: "sched-interactive", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityInteractive}},
	}); err != nil {
		t.Fatalf("enqueue interactive: %v", err)
	}

	select {
	case startedAt := <-interactiveStarted:
		wait := startedAt.Sub(enqueuedAt)
		t.Logf("interactive step started %s after being enqueued", wait)
		// One chunk (2 batch steps * 1s, since MaxWorkers=1 serializes
		// them) plus generous scheduling overhead.
		if wait > 2*chunkStepDuration+2*time.Second {
			t.Fatalf("interactive step waited %s, want at most one chunk (~%s) plus overhead", wait, 2*chunkStepDuration)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("interactive step never started")
	}
}
