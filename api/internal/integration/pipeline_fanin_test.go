//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// TestFanInConcurrentDepCompletionEnqueuesDependentExactlyOnce drives 40
// leaf steps to completion concurrently, all fanning into one join step,
// and asserts the join is queued and has exactly one live River job: the
// lost-wakeup race this guards is two leaves finishing at nearly the same
// instant both observing remaining_deps > 0 right before decrementing,
// neither realising it was the one to hit zero.
func TestFanInConcurrentDepCompletionEnqueuesDependentExactlyOnce(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("fanin-leaf", pipeline.QueueCPU))
	registry.Register(succeedsImmediately("fanin-join", pipeline.QueueCPU))
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "fanin-tenant")

	const leaves = 40
	runID := idconv.NewV7()
	joinID := idconv.NewV7()
	leafIDs := make([]uuid.UUID, leaves)
	steps := make([]pipeline.StepSpec, 0, leaves+1)
	for i := 0; i < leaves; i++ {
		leafIDs[i] = idconv.NewV7()
		steps = append(steps, pipeline.StepSpec{
			ID: leafIDs[i], Kind: "fanin-leaf", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch,
		})
	}
	steps = append(steps, pipeline.StepSpec{
		ID: joinID, Kind: "fanin-join", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch, DependsOn: leafIDs,
	})

	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "fanin-test", Steps: steps,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if row := readStep(t, pool, joinID); row.Status != pipeline.StatusPending || row.RemainingDeps != leaves {
		t.Fatalf("expected the join step to start pending with %d remaining deps, got %+v", leaves, row)
	}

	// Complete every leaf concurrently through the real dispatch path
	// (claim -> run -> commit -> fan-in), one synthetic job id per leaf so
	// each runs its own independent transaction, exactly like distinct
	// River job fetches would.
	runConcurrently(leaves, func(i int) {
		if err := engine.Dispatch(context.Background(), int64(1000+i), []uuid.UUID{leafIDs[i]}, pipeline.DispatchOpts{}); err != nil {
			t.Errorf("dispatch leaf %d: %v", i, err)
		}
	})

	join := readStep(t, pool, joinID)
	if join.Status != pipeline.StatusQueued {
		t.Fatalf("expected the join step to be queued after every leaf completed, got status %q remaining_deps %d", join.Status, join.RemainingDeps)
	}
	if join.RemainingDeps != 0 {
		t.Fatalf("expected remaining_deps to be exactly 0, got %d", join.RemainingDeps)
	}
	if n := countRiverJobsForStep(t, pool, joinID); n != 1 {
		t.Fatalf("expected the join step to have exactly one live River job, got %d", n)
	}
}

// TestBatchPartialFailureMarksOnlyTheFailedStep chunks three sibling
// steps into one job (via a custom estimator) where one permanently
// fails; the job itself must still report success (nil), and only the
// failing step is marked failed.
func TestBatchPartialFailureMarksOnlyTheFailedStep(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(&fakeHandler{kind: "batch-ok", queue: pipeline.QueueCPU, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		return pipeline.Output{}, nil
	}})
	registry.Register(&fakeHandler{kind: "batch-bad", queue: pipeline.QueueCPU, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		return nil, pipeline.ErrValidation
	}})
	engine, pool := pipelineEngine(t, registry)
	// Enqueue's own chunking is irrelevant to this test: it asserts on a
	// single manual Dispatch call covering all three ids at once, exactly
	// modelling one River job whose chunk happened to include a step that
	// permanently fails alongside two that succeed.
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "batch-tenant")

	runID := idconv.NewV7()
	ids := []uuid.UUID{idconv.NewV7(), idconv.NewV7(), idconv.NewV7()}
	kinds := []string{"batch-ok", "batch-ok", "batch-bad"}
	steps := make([]pipeline.StepSpec, 3)
	for i := range ids {
		steps[i] = pipeline.StepSpec{ID: ids[i], Kind: kinds[i], ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch}
	}
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "batch-test", Steps: steps}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := engine.Dispatch(context.Background(), 5001, ids, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("expected the job to report success despite one permanent step failure, got %v", err)
	}

	if s := readStep(t, pool, ids[0]); s.Status != pipeline.StatusDone {
		t.Fatalf("expected step 0 done, got %q", s.Status)
	}
	if s := readStep(t, pool, ids[1]); s.Status != pipeline.StatusDone {
		t.Fatalf("expected step 1 done, got %q", s.Status)
	}
	if s := readStep(t, pool, ids[2]); s.Status != pipeline.StatusFailed {
		t.Fatalf("expected step 2 failed, got %q", s.Status)
	}
}
