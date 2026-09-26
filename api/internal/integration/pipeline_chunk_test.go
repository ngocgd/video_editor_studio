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

// TestDispatchClaimsChunkStepsLazilyOneAtATime drives Dispatch with a
// 3-step chunk where the first step's handler blocks until released, and
// asserts the other two steps are still "queued" (never claimed) while
// it blocks. Claiming the whole chunk up front — before working any of
// it — would mark every step "running" immediately with only the first
// one's heartbeat goroutine actually running; the others would sit with
// a stale claim-time heartbeat the whole time step one runs, eligible
// for the reconciler to reclaim and hand to a second worker while this
// same call is still going to reach them, running the same step twice
// concurrently.
func TestDispatchClaimsChunkStepsLazilyOneAtATime(t *testing.T) {
	skipIfAPIUnreachable(t)
	var callCount atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	registry := pipeline.NewRegistry()
	registry.Register(&fakeHandler{kind: "chunk-lazy", queue: testQueue, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		if callCount.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return pipeline.Output{}, nil
	}})
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "chunk-lazy-tenant")

	runID := idconv.NewV7()
	ids := []uuid.UUID{idconv.NewV7(), idconv.NewV7(), idconv.NewV7()}
	steps := make([]pipeline.StepSpec, len(ids))
	for i, id := range ids {
		steps[i] = pipeline.StepSpec{ID: id, Kind: "chunk-lazy", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch}
	}
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "chunk-lazy-test", Steps: steps,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- engine.Dispatch(context.Background(), 55001, ids, pipeline.DispatchOpts{})
	}()

	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first step in the chunk never started")
	}

	if s := readStep(t, pool, ids[0]); s.Status != pipeline.StatusRunning {
		t.Fatalf("expected the first step to be running, got %q", s.Status)
	}
	for i := 1; i < len(ids); i++ {
		if s := readStep(t, pool, ids[i]); s.Status != pipeline.StatusQueued {
			t.Fatalf("expected step %d to still be queued (not yet claimed) while step 0 runs, got %q", i, s.Status)
		}
	}

	close(releaseFirst)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch never finished after releasing the first step")
	}

	for i, id := range ids {
		if s := readStep(t, pool, id); s.Status != pipeline.StatusDone {
			t.Fatalf("expected step %d done after the chunk finished, got %q", i, s.Status)
		}
	}
	if got := callCount.Load(); got != int32(len(ids)) {
		t.Fatalf("expected the handler to run exactly once per step (%d), ran %d times", len(ids), got)
	}
}

// TestDispatchSnoozesInsteadOfDestroyingAnUnregisteredKind models a
// worker process (an old binary mid-rollout, or — as in this repo today
// — the shared stack's real worker container, whose registry is empty
// until a later phase registers a handler) that has no handler for a
// step's kind: it must never claim (and thereby permanently fail) that
// step. It snoozes the whole job instead, and the step is left exactly
// as it was, still claimable once a capable worker exists.
func TestDispatchSnoozesInsteadOfDestroyingAnUnregisteredKind(t *testing.T) {
	skipIfAPIUnreachable(t)
	registryWithHandler := pipeline.NewRegistry()
	registryWithHandler.Register(succeedsImmediately("unregistered-elsewhere", testQueue))
	engine, pool := pipelineEngine(t, registryWithHandler)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "unregistered-kind-tenant")

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "unregistered-kind-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "unregistered-elsewhere", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// A second engine, standing in for a different worker process (or the
	// same process before a later phase's handler registration lands),
	// with no handler at all for this kind.
	emptyRegistry := pipeline.NewRegistry()
	incapableEngine, _ := pipelineEngineOnPool(t, emptyRegistry, pool)

	err := incapableEngine.Dispatch(context.Background(), 66001, []uuid.UUID{stepID}, pipeline.DispatchOpts{})
	var snooze *river.JobSnoozeError
	if !errors.As(err, &snooze) {
		t.Fatalf("expected a JobSnoozeError from a worker with no handler for this kind, got %v", err)
	}

	after := readStep(t, pool, stepID)
	if after.Status != pipeline.StatusQueued {
		t.Fatalf("expected the step to be left exactly as it was (queued), got %q", after.Status)
	}
	if after.Attempt != 0 {
		t.Fatalf("expected the incapable worker to never have claimed (and so never incremented) the step, attempt=%d", after.Attempt)
	}

	// A capable engine (the real handler is registered) can still claim
	// and finish it normally afterwards.
	if err := engine.Dispatch(context.Background(), 66002, []uuid.UUID{stepID}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch with the capable engine: %v", err)
	}
	if s := readStep(t, pool, stepID); s.Status != pipeline.StatusDone {
		t.Fatalf("expected the step done once a capable worker ran it, got %q", s.Status)
	}
}
