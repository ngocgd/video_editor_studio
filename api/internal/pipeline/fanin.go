package pipeline

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
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

// MarkStaleDependents re-arms every step listed in ids after an upstream
// step was reset to queued outside the normal fan-in path (e.g. a
// per-scene rerun): a "done" dependent is reopened to "pending" first —
// its old output was computed against inputs that are about to change,
// so it must be re-run too, not left "done" forever showing stale
// output — then remaining_deps is recomputed from pipeline_step_deps for
// every id, and anything that comes out already satisfied (its other
// dependencies, if any, were already done) is swept straight to queued
// and enqueued, the same as the reconciler's own ready sweep would
// eventually do, just without waiting up to reconcileInterval for it.
func (e *Engine) MarkStaleDependents(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	pgTenant := idconv.ToPg(tenantID)
	pgIDs := toPgUUIDs(ids)

	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := e.Queries.WithTx(tx)

	if _, err := qtx.MarkStepsPending(ctx, dbgen.MarkStepsPendingParams{TenantID: pgTenant, Ids: pgIDs}); err != nil {
		return err
	}

	var readyIDs []pgtype.UUID
	for _, id := range ids {
		recomputed, err := qtx.RecomputeRemainingDeps(ctx, dbgen.RecomputeRemainingDepsParams{TenantID: pgTenant, ID: idconv.ToPg(id)})
		if err != nil {
			return err
		}
		if recomputed.Status == StatusPending && recomputed.RemainingDeps <= 0 {
			readyIDs = append(readyIDs, recomputed.ID)
		}
	}
	if len(readyIDs) == 0 {
		return tx.Commit(ctx)
	}

	queued, err := qtx.MarkStepsQueued(ctx, dbgen.MarkStepsQueuedParams{TenantID: pgTenant, Ids: readyIDs})
	if err != nil {
		return err
	}
	if len(queued) > 0 {
		if err := e.enqueueReadySteps(ctx, tx, queued); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, s := range queued {
		publishStepEvent(ctx, e.Pool, s, true)
	}
	return nil
}
