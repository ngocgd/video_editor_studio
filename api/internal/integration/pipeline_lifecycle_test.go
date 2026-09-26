//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// runStatus reads a run's current status straight from Postgres.
func runStatus(t *testing.T, pool *pgxpool.Pool, runID uuid.UUID) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM pipeline_runs WHERE id = $1`, runID).Scan(&status); err != nil {
		t.Fatalf("read run status: %v", err)
	}
	return status
}

// TestRunRollsUpToDoneWhenEveryStepFinishes covers M8: nothing rolled a
// run up to "done" or "failed" before; a run with two independent steps
// that both succeed must end up "done" on its own, with no separate
// rollup call from the test.
func TestRunRollsUpToDoneWhenEveryStepFinishes(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("rollup-ok", testQueue))
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "rollup-done-tenant")

	runID := idconv.NewV7()
	stepA, stepB := idconv.NewV7(), idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "rollup-done-test",
		Steps: []pipeline.StepSpec{
			{ID: stepA, Kind: "rollup-ok", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
			{ID: stepB, Kind: "rollup-ok", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
		},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := engine.Dispatch(context.Background(), 71001, []uuid.UUID{stepA}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch A: %v", err)
	}
	if got := runStatus(t, pool, runID); got != "active" {
		t.Fatalf("expected the run to stay active with one step still outstanding, got %q", got)
	}
	if err := engine.Dispatch(context.Background(), 71002, []uuid.UUID{stepB}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch B: %v", err)
	}
	if got := runStatus(t, pool, runID); got != "done" {
		t.Fatalf("expected the run to roll up to done once every step finished, got %q", got)
	}
}

// TestPermanentFailureCascadeCancelsDependentsAndFailsTheRun covers both
// halves of M8/M9 together: a permanently failed step's "pending"
// dependent (which can never become ready on its own — its one
// dependency will never produce output) is cascade-cancelled, and the
// run then rolls up to "failed" instead of sitting "active" forever with
// a dependent stuck "pending".
func TestPermanentFailureCascadeCancelsDependentsAndFailsTheRun(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(&fakeHandler{kind: "rollup-bad", queue: testQueue, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		return nil, pipeline.ErrValidation
	}})
	registry.Register(succeedsImmediately("rollup-dependent", testQueue))
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "rollup-failed-tenant")

	runID := idconv.NewV7()
	upstream, downstream := idconv.NewV7(), idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "rollup-failed-test",
		Steps: []pipeline.StepSpec{
			{ID: upstream, Kind: "rollup-bad", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
			{ID: downstream, Kind: "rollup-dependent", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch, DependsOn: []uuid.UUID{upstream}},
		},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := engine.Dispatch(context.Background(), 72001, []uuid.UUID{upstream}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch upstream: %v", err)
	}

	if s := readStep(t, pool, upstream); s.Status != pipeline.StatusFailed {
		t.Fatalf("expected upstream failed, got %q", s.Status)
	}
	if s := readStep(t, pool, downstream); s.Status != pipeline.StatusCanceled {
		t.Fatalf("expected the downstream step to be cascade-cancelled once its only dependency permanently failed, got %q", s.Status)
	}
	if got := runStatus(t, pool, runID); got != "failed" {
		t.Fatalf("expected the run to roll up to failed, got %q", got)
	}
}

// TestRetryStepRejectsInactiveRun covers H4: a step in a run that already
// finished (rolled up to "failed" here) can no longer be retried through
// the normal path — the caller must start a new run instead.
func TestRetryStepRejectsInactiveRun(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(&fakeHandler{kind: "retry-inactive-run", queue: testQueue, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		return nil, pipeline.ErrValidation
	}})
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "retry-inactive-tenant")

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "retry-inactive-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "retry-inactive-run", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := engine.Dispatch(context.Background(), 73001, []uuid.UUID{stepID}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := runStatus(t, pool, runID); got != "failed" {
		t.Fatalf("expected the run to roll up to failed (sanity check), got %q", got)
	}

	_, err := engine.RetryStep(context.Background(), tenantID, stepID)
	if !errors.Is(err, pipeline.ErrRetryNotAllowed) {
		t.Fatalf("expected ErrRetryNotAllowed for a step whose run already finished, got %v", err)
	}
}

// TestRetryStepRejectsUnmetDependency covers the other half of H4: a
// step whose remaining_deps is not yet zero (recomputed after it already
// failed, e.g. by MarkStaleDependents re-arming it against a changed
// upstream) cannot be retried until its dependency is satisfied again.
func TestRetryStepRejectsUnmetDependency(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(&fakeHandler{kind: "retry-unmet-dep", queue: testQueue, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		return nil, pipeline.ErrValidation
	}})
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "retry-unmet-dep-tenant")

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "retry-unmet-dep-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "retry-unmet-dep", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := engine.Dispatch(context.Background(), 74001, []uuid.UUID{stepID}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if s := readStep(t, pool, stepID); s.Status != pipeline.StatusFailed {
		t.Fatalf("expected the step failed (sanity check), got %q", s.Status)
	}

	// The run rolled up to "failed" too (its only step failed), which
	// would already reject the retry on its own; reopen it back to
	// "active" so this test isolates the remaining_deps guard
	// specifically, matching a per-scene rerun that reactivates a run
	// before re-arming one of its steps.
	if _, err := pool.Exec(context.Background(), `UPDATE pipeline_runs SET status = 'active' WHERE id = $1`, runID); err != nil {
		t.Fatalf("reactivate run: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE pipeline_steps SET remaining_deps = 1 WHERE id = $1`, stepID); err != nil {
		t.Fatalf("simulate an unmet dependency: %v", err)
	}

	_, err := engine.RetryStep(context.Background(), tenantID, stepID)
	if !errors.Is(err, pipeline.ErrRetryNotAllowed) {
		t.Fatalf("expected ErrRetryNotAllowed for a step with an unmet dependency, got %v", err)
	}
}

// TestMarkStaleDependentsReopensDoneStepAndReArmsIt covers M9: a "done"
// dependent must be reopened to "pending" (its output was computed
// against inputs about to change) and, once its own remaining_deps
// recomputes to zero, swept straight back to "queued" with a fresh
// River job — not left "done" showing stale output forever.
func TestMarkStaleDependentsReopensDoneStepAndReArmsIt(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("stale-dep-upstream", testQueue))
	registry.Register(succeedsImmediately("stale-dep-downstream", testQueue))
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "stale-dep-tenant")

	runID := idconv.NewV7()
	upstream, downstream := idconv.NewV7(), idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "stale-dep-test",
		Steps: []pipeline.StepSpec{
			{ID: upstream, Kind: "stale-dep-upstream", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
			{ID: downstream, Kind: "stale-dep-downstream", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch, DependsOn: []uuid.UUID{upstream}},
		},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := engine.Dispatch(context.Background(), 75001, []uuid.UUID{upstream}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch upstream: %v", err)
	}
	if err := engine.Dispatch(context.Background(), 75002, []uuid.UUID{downstream}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch downstream: %v", err)
	}
	if s := readStep(t, pool, downstream); s.Status != pipeline.StatusDone {
		t.Fatalf("expected downstream done (sanity check), got %q", s.Status)
	}
	// A real river.Client would have already marked downstream's first
	// job "completed" once its Work call returned nil; these tests drive
	// Dispatch directly instead, so that never happens on its own.
	completeRiverJobsForStep(t, pool, downstream)

	// The upstream is reset to queued (a per-scene rerun would do this);
	// MarkStaleDependents must reopen the already-"done" downstream and
	// re-arm it.
	forceStatus(t, pool, upstream, pipeline.StatusQueued)
	if err := engine.MarkStaleDependents(context.Background(), tenantID, []uuid.UUID{downstream}); err != nil {
		t.Fatalf("mark stale dependents: %v", err)
	}
	if s := readStep(t, pool, downstream); s.Status != pipeline.StatusPending {
		t.Fatalf("expected the downstream step reopened to pending (its dependency is not done yet), got %q", s.Status)
	}

	// Once the upstream finishes again, the downstream must fan back in
	// to queued and get a fresh job, exactly like a first run.
	if err := engine.Dispatch(context.Background(), 75003, []uuid.UUID{upstream}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("re-dispatch upstream: %v", err)
	}
	if s := readStep(t, pool, downstream); s.Status != pipeline.StatusQueued {
		t.Fatalf("expected the downstream step queued again once its dependency finished, got %q", s.Status)
	}
	if n := countRiverJobsForStep(t, pool, downstream); n != 1 {
		t.Fatalf("expected exactly one live River job for the re-armed step, got %d", n)
	}
}
