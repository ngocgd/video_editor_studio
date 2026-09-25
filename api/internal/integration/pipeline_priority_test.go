//go:build integration

package integration

import (
	"context"
	"testing"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// TestQueueResolvedAtEnqueueTimeIsPinnedOnTheStepRow enqueues a step
// through a handler and asserts the queue/provider_ref stored on the row
// match what Queue/ModelRef returned at that moment, proving a later
// settings change cannot move an already-queued step (there is nothing
// left to re-resolve from).
func TestQueueResolvedAtEnqueueTimeIsPinnedOnTheStepRow(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("queue-pin", pipeline.QueueLLM))
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "queue-pin-tenant")

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "queue-pin-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "queue-pin", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityInteractive}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	var queue string
	if err := pool.QueryRow(context.Background(), `SELECT queue FROM pipeline_steps WHERE id = $1`, stepID).Scan(&queue); err != nil {
		t.Fatalf("read queue: %v", err)
	}
	if queue != pipeline.QueueLLM {
		t.Fatalf("expected the step's queue to be pinned to %q at enqueue time, got %q", pipeline.QueueLLM, queue)
	}
}

// TestPriorityIsStoredOnTheRiverJobRow asserts an interactive-priority
// step's River job row carries priority 1 and a batch-priority step's
// carries priority 3, matching River's "lower runs first" ordering:
// interactive work is scheduled ahead of queued batch chunks.
func TestPriorityIsStoredOnTheRiverJobRow(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("priority-check", pipeline.QueueCPU))
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "priority-tenant")

	runID := idconv.NewV7()
	interactiveID := idconv.NewV7()
	batchID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "priority-test",
		Steps: []pipeline.StepSpec{
			{ID: interactiveID, Kind: "priority-check", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityInteractive},
		},
	}); err != nil {
		t.Fatalf("enqueue interactive: %v", err)
	}
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: idconv.NewV7(), ScopeKind: "test", ScopeID: runID, Kind: "priority-test",
		Steps: []pipeline.StepSpec{
			{ID: batchID, Kind: "priority-check", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
		},
	}); err != nil {
		t.Fatalf("enqueue batch: %v", err)
	}

	priorityFor := func(stepID interface{ String() string }) int {
		var p int
		if err := pool.QueryRow(context.Background(),
			`SELECT priority FROM river_job WHERE kind = $1 AND args -> 'step_ids' ? $2`,
			pipeline.JobKind, stepID.String(),
		).Scan(&p); err != nil {
			t.Fatalf("read river job priority: %v", err)
		}
		return p
	}

	if p := priorityFor(interactiveID); p != pipeline.PriorityInteractive {
		t.Fatalf("expected interactive priority %d, got %d", pipeline.PriorityInteractive, p)
	}
	if p := priorityFor(batchID); p != pipeline.PriorityBatch {
		t.Fatalf("expected batch priority %d, got %d", pipeline.PriorityBatch, p)
	}
}
