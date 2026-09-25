package pipeline

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// Run-level statuses, matching pipeline_runs.status's CHECK constraint.
// Distinct from the step Status* constants in transition.go.
const (
	RunStatusDone   = "done"
	RunStatusFailed = "failed"
)

// cascadeCancelPendingDependents walks the dependency graph downstream
// from a permanently failed step, cancelling every "pending" dependent
// it finds (breadth-first, since pipeline_step_deps is a DAG): a step
// whose upstream will never produce output can never satisfy its own
// dependency on its own, so leaving it "pending" forever would also
// leave the run stuck "active" forever (rollupRun never sees the
// non-terminal count reach zero). Returns every step it cancelled, for
// the caller to publish events for.
func (e *Engine) cascadeCancelPendingDependents(ctx context.Context, tenantID pgtype.UUID, failedStepID pgtype.UUID) []dbgen.PipelineStep {
	var all []dbgen.PipelineStep
	frontier := []pgtype.UUID{failedStepID}
	for len(frontier) > 0 {
		deps, err := e.Queries.GetDependents(ctx, dbgen.GetDependentsParams{TenantID: tenantID, Ids: frontier})
		if err != nil {
			slog.ErrorContext(ctx, "pipeline: cascade-cancel: failed to load dependents", "error", err)
			return all
		}
		if len(deps) == 0 {
			return all
		}
		canceled, err := e.Queries.CancelPendingDependents(ctx, dbgen.CancelPendingDependentsParams{TenantID: tenantID, Ids: deps})
		if err != nil {
			slog.ErrorContext(ctx, "pipeline: cascade-cancel: failed to cancel dependents", "error", err)
			return all
		}
		if len(canceled) == 0 {
			return all
		}
		all = append(all, canceled...)
		frontier = frontier[:0]
		for _, s := range canceled {
			frontier = append(frontier, s.ID)
		}
	}
	return all
}

// rollupRun marks runID done or failed once every one of its steps has
// reached a terminal state (done, failed or canceled), and is a no-op if
// the run is not "active" any more (already cancelled/superseded, or
// this rollup lost a race with another one — MarkRunStatusIfNotTerminal
// is itself the fence). Called after every step commit; cheap even when
// it finds nothing to do, since CountNonTerminalStepsInRun is indexed by
// (run_id, status).
func (e *Engine) rollupRun(ctx context.Context, tenantID, runID pgtype.UUID) {
	remaining, err := e.Queries.CountNonTerminalStepsInRun(ctx, dbgen.CountNonTerminalStepsInRunParams{TenantID: tenantID, RunID: runID})
	if err != nil {
		slog.ErrorContext(ctx, "pipeline: rollup: failed to count non-terminal steps", "error", err)
		return
	}
	if remaining > 0 {
		return
	}

	status := RunStatusDone
	if failed, err := e.Queries.HasFailedStepsInRun(ctx, dbgen.HasFailedStepsInRunParams{TenantID: tenantID, RunID: runID}); err != nil {
		slog.ErrorContext(ctx, "pipeline: rollup: failed to check for failed steps", "error", err)
		return
	} else if failed {
		status = RunStatusFailed
	}

	if _, err := e.Queries.MarkRunStatusIfNotTerminal(ctx, dbgen.MarkRunStatusIfNotTerminalParams{
		Status: status, TenantID: tenantID, ID: runID,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "pipeline: rollup: failed to mark run status", "error", err, "run_id", idconv.FromPg(runID), "status", status)
	}
}
