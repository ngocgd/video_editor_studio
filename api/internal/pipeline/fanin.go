package pipeline

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
)

// fanIn runs the lost-wakeup-free dependency resolution for a just
// completed step, inside the same transaction as its output commit: it
// decrements remaining_deps on every dependent, moves any dependent that
// reaches zero from pending to queued, and enqueues a River job for each
// newly ready group. It returns the dependents that became queued so the
// caller can publish their transition events after commit.
func (e *Engine) fanIn(ctx context.Context, qtx *dbgen.Queries, tx pgx.Tx, completed dbgen.PipelineStep) ([]dbgen.PipelineStep, error) {
	dependents, err := qtx.GetDependents(ctx, dbgen.GetDependentsParams{
		TenantID: completed.TenantID,
		Ids:      []pgtype.UUID{completed.ID},
	})
	if err != nil {
		return nil, err
	}
	if len(dependents) == 0 {
		return nil, nil
	}

	decremented, err := qtx.DecrementRemainingDeps(ctx, dbgen.DecrementRemainingDepsParams{
		TenantID: completed.TenantID,
		Ids:      dependents,
	})
	if err != nil {
		return nil, err
	}

	var readyIDs []pgtype.UUID
	for _, s := range decremented {
		if s.Status == StatusPending && s.RemainingDeps <= 0 {
			readyIDs = append(readyIDs, s.ID)
		}
	}
	if len(readyIDs) == 0 {
		return nil, nil
	}

	queued, err := qtx.MarkStepsQueued(ctx, dbgen.MarkStepsQueuedParams{
		TenantID: completed.TenantID,
		Ids:      readyIDs,
	})
	if err != nil {
		return nil, err
	}
	if len(queued) == 0 {
		return nil, nil
	}
	if err := e.enqueueReadySteps(ctx, tx, queued); err != nil {
		return nil, err
	}
	return queued, nil
}

// MarkStaleDependents recomputes remaining_deps for every step listed in
// ids from pipeline_step_deps, used by the reconciler and by a per-scene
// rerun to re-arm dependents whose upstream step was reset to queued
// outside the normal fan-in path (a stale step becoming re-runnable is
// not itself a "dependency just completed" event).
func (e *Engine) MarkStaleDependents(ctx context.Context, tenantID pgtype.UUID, ids []pgtype.UUID) error {
	for _, id := range ids {
		if _, err := e.Queries.RecomputeRemainingDeps(ctx, dbgen.RecomputeRemainingDepsParams{
			TenantID: tenantID,
			ID:       id,
		}); err != nil {
			return err
		}
	}
	return nil
}
