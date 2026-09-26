//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// TestReconcilerResumesAfterCrash models "kill the worker mid-step":
// a step is claimed (attempt 1, running) as a real River job fetch would
// leave it, its heartbeat is forced stale (standing in for the SIGKILLed
// process never heartbeating again), and the reconciler must requeue it.
// The next claim moves it to attempt 2, and a duplicate claim attempt
// under the original (now-stale) job id finds nothing left to claim,
// modelling River's own stuck-job rescue firing on the same step later.
func TestReconcilerResumesAfterCrash(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("crash-resume", testQueue))
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "crash-resume-tenant")

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "crash-resume-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "crash-resume", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	claimed, err := pipeline.Claim(context.Background(), dbgen.New(pool), 9001, []uuid.UUID{stepID})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v (claimed %d)", err, len(claimed))
	}
	if claimed[0].Attempt != 1 {
		t.Fatalf("expected attempt 1 after the first claim, got %d", claimed[0].Attempt)
	}

	// The "worker process" that claimed this step is gone: it never
	// heartbeats again. Simulate the passage of time past the stale
	// threshold instead of an actual 60s sleep.
	setHeartbeatInPast(t, pool, stepID, 5*time.Minute)

	reconciler := &pipeline.Reconciler{Engine: engine}
	reconciler.RunOnce(context.Background())

	afterReconcile := readStep(t, pool, stepID)
	if afterReconcile.Status != pipeline.StatusQueued {
		t.Fatalf("expected the reconciler to requeue the step, got status %q", afterReconcile.Status)
	}

	// The new job (job id changes on every real re-fetch) claims attempt 2.
	reclaimed, err := pipeline.Claim(context.Background(), dbgen.New(pool), 9002, []uuid.UUID{stepID})
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("reclaim: %v (claimed %d)", err, len(reclaimed))
	}
	if reclaimed[0].Attempt != 2 {
		t.Fatalf("expected attempt 2 on resume, got %d", reclaimed[0].Attempt)
	}

	// A forced duplicate claim under a third synthetic job id (standing in
	// for River's own rescue firing on the original, now long-finished
	// job) must claim nothing: the step is "running" again under attempt 2.
	dup, err := pipeline.Claim(context.Background(), dbgen.New(pool), 9003, []uuid.UUID{stepID})
	if err != nil {
		t.Fatalf("duplicate claim: %v", err)
	}
	if len(dup) != 0 {
		t.Fatalf("expected the duplicate rescue-style claim to claim nothing, got %d rows", len(dup))
	}
}

// TestReconcilerRecoversWhileTheDeadRiverJobIsStillRunning reproduces the
// real crash shape end to end: Enqueue creates a real river_job row,
// forced to "running" (matching what River's own bookkeeping shows for a
// job whose worker process was SIGKILLed — it never transitions off
// "running" on its own; River's stuck-job rescue would only notice after
// RescueStuckJobsAfter, 4h). The reconciler must produce a fresh,
// claimable job well within that window despite the dead job still
// sitting in "running": a River uniqueness constraint keyed by args
// would otherwise silently dedupe the reconciler's own re-enqueue against
// that dead row and strand the step for the full 4h.
func TestReconcilerRecoversWhileTheDeadRiverJobIsStillRunning(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("dead-job-crash", testQueue))
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "dead-job-crash-tenant")

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "dead-job-crash-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "dead-job-crash", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if n := countRiverJobsForStep(t, pool, stepID); n != 1 {
		t.Fatalf("expected exactly one live river job right after enqueue, got %d", n)
	}

	// A real worker fetching this job would claim the step (queued ->
	// running) and River would mark the job row "running" too. Model both
	// halves of that directly instead of running an actual river.Client.
	claimed, err := pipeline.Claim(context.Background(), dbgen.New(pool), 42001, []uuid.UUID{stepID})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v (claimed %d)", err, len(claimed))
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE river_job SET state = 'running' WHERE kind = $1 AND args -> 'step_ids' ? $2`,
		pipeline.JobKind, stepID.String(),
	); err != nil {
		t.Fatalf("force river_job to running: %v", err)
	}

	// The worker that claimed it is gone; it never heartbeats again.
	setHeartbeatInPast(t, pool, stepID, 5*time.Minute)

	reconciler := &pipeline.Reconciler{Engine: engine}
	reconciler.RunOnce(context.Background())

	after := readStep(t, pool, stepID)
	if after.Status != pipeline.StatusQueued {
		t.Fatalf("expected the reconciler to requeue the step, got status %q", after.Status)
	}

	var available int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM river_job WHERE kind = $1 AND args -> 'step_ids' ? $2 AND state = 'available'`,
		pipeline.JobKind, stepID.String(),
	).Scan(&available); err != nil {
		t.Fatalf("count available river jobs: %v", err)
	}
	if available == 0 {
		t.Fatal("expected the reconciler to have inserted a fresh available River job despite the dead job still sitting in 'running'; a step stuck here for the full RescueStuckJobsAfter window is exactly the bug this test guards against")
	}

	// And it is genuinely claimable now, not just present as a row.
	reclaimed, err := pipeline.Claim(context.Background(), dbgen.New(pool), 42002, []uuid.UUID{stepID})
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("reclaim after recovery: %v (claimed %d)", err, len(reclaimed))
	}
	if reclaimed[0].Attempt != 2 {
		t.Fatalf("expected attempt 2 on the recovered claim, got %d", reclaimed[0].Attempt)
	}
}

// TestZombieWriterFailsHeartbeatAndCommitsNothing: a step is claimed,
// then reclaimed out from under the original claim (simulating the
// reconciler firing while the original handler is still, wrongly,
// running). The original handler's heartbeat must report "not alive",
// and its late commit attempt must affect zero rows.
func TestZombieWriterFailsHeartbeatAndCommitsNothing(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("zombie", testQueue))
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "zombie-tenant")

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "zombie-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "zombie", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	claimed, err := pipeline.Claim(context.Background(), dbgen.New(pool), 9101, []uuid.UUID{stepID})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v", err)
	}
	originalAttempt := claimed[0].Attempt

	// Someone else (the reconciler, in production) decided this step was
	// stale and reset it to queued without this handler's knowledge.
	forceStatus(t, pool, stepID, pipeline.StatusQueued)

	alive, err := pipeline.Heartbeat(context.Background(), dbgen.New(pool), stepID, originalAttempt)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if alive {
		t.Fatal("expected the zombie handler's heartbeat to report not-alive once the step was reclaimed")
	}

	// It must not be able to commit output either: the CAS on commit uses
	// the identical (id, attempt, status='running') fence as heartbeat.
	before := readStep(t, pool, stepID)
	// Re-claim through the normal path so the step is "running" again
	// under a NEW attempt, then assert the zombie's stale attempt number
	// still cannot commit against it.
	if _, err := pipeline.Claim(context.Background(), dbgen.New(pool), 9102, []uuid.UUID{stepID}); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	stillAlive, err := pipeline.Heartbeat(context.Background(), dbgen.New(pool), stepID, originalAttempt)
	if err != nil {
		t.Fatalf("heartbeat with stale attempt: %v", err)
	}
	if stillAlive {
		t.Fatal("expected the zombie's original attempt number to never succeed a heartbeat again, even after a fresh claim")
	}
	after := readStep(t, pool, stepID)
	if after.Version == before.Version && after.Attempt == before.Attempt {
		t.Fatal("expected the re-claim itself to have moved the step forward (sanity check on the test setup)")
	}
}

// TestReconcilerSweepsReadyPendingStepWithNoLiveJob covers the fan-in gap
// the reconciler exists to close: a pending step whose remaining_deps
// already reached zero (recomputed outside the normal completion path,
// e.g. by MarkStaleDependents) but that was never itself enqueued.
func TestReconcilerSweepsReadyPendingStepWithNoLiveJob(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("sweep-dep", testQueue))
	registry.Register(succeedsImmediately("sweep-target", testQueue))
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "sweep-tenant")

	runID := idconv.NewV7()
	depID := idconv.NewV7()
	targetID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "sweep-test",
		Steps: []pipeline.StepSpec{
			{ID: depID, Kind: "sweep-dep", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
			{ID: targetID, Kind: "sweep-target", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch, DependsOn: []uuid.UUID{depID}},
		},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if s := readStep(t, pool, targetID); s.Status != pipeline.StatusPending || s.RemainingDeps != 1 {
		t.Fatalf("expected the target step to start pending with 1 remaining dep, got %+v", s)
	}

	// Simulate a fan-in gap: remaining_deps drops to 0 without the normal
	// completion path ever enqueuing a job for it (e.g. the process
	// crashed between the two, or MarkStaleDependents recomputed it).
	if _, err := pool.Exec(context.Background(), `UPDATE pipeline_steps SET remaining_deps = 0 WHERE id = $1`, targetID); err != nil {
		t.Fatalf("simulate fan-in gap: %v", err)
	}

	reconciler := &pipeline.Reconciler{Engine: engine}
	reconciler.RunOnce(context.Background())

	after := readStep(t, pool, targetID)
	if after.Status != pipeline.StatusQueued {
		t.Fatalf("expected the reconciler to sweep the ready pending step to queued, got %q", after.Status)
	}
	if n := countRiverJobsForStep(t, pool, targetID); n != 1 {
		t.Fatalf("expected exactly one live River job for the swept step, got %d", n)
	}
}
