package pipeline

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// maxStrandedRequeues bounds how many times the reconciler will
// re-enqueue a "queued" step it finds with no live River job before
// giving up and failing it outright: past this point, re-enqueuing
// indefinitely would just repeat whatever earlier failure mode left the
// step without a job in the first place.
const maxStrandedRequeues = 5

// riverLiveStates are the River job states that count as "this step
// still has a job"; anything else (completed, cancelled, discarded) does
// not, and a "queued" step with no row in any of these states is
// orphaned.
var riverLiveStates = []string{"available", "scheduled", "running", "retryable", "pending"}

// sweepOrphanedQueued finds "queued" steps with no corresponding live
// River job (river_job is River's own schema, not visible to
// sqlc/goose, so the check here is hand-written SQL) and either
// re-enqueues them (incrementing their stranded_requeues budget) or, past
// that budget, fails them outright with a clear error_code so they stop
// holding quota and the failure is visible instead of silent.
//
// This is a defensive fallback, not the primary recovery path: the
// normal transient-failure and gpu_oom paths already requeue a step only
// when a River retry of the same job will exist to reclaim it (see
// dispatcher.go), and every place that flips a step to "queued" inserts
// its own River job in the same transaction. Reaching this sweep means
// something outside that contract happened (a River job manually
// deleted, an unexpected panic path, or a future bug elsewhere).
func (r *Reconciler) sweepOrphanedQueued(ctx context.Context) error {
	for {
		candidates, err := r.Engine.Queries.OrphanedQueuedStepsBatch(ctx, dbgen.OrphanedQueuedStepsBatchParams{
			MaxStrandedRequeues: maxStrandedRequeues,
			PageLimit:           reconcileBatchLimit,
		})
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}

		for _, step := range candidates {
			if err := r.reconcileOneOrphan(ctx, step); err != nil {
				slog.ErrorContext(ctx, "pipeline: failed to reconcile a candidate orphaned step", "step_id", idconv.FromPg(step.ID), "error", err)
			}
		}
		if len(candidates) < reconcileBatchLimit {
			return nil
		}
	}
}

func (r *Reconciler) reconcileOneOrphan(ctx context.Context, step dbgen.PipelineStep) error {
	hasJob, err := r.Engine.hasLiveRiverJob(ctx, idconv.FromPg(step.ID))
	if err != nil {
		return err
	}
	if hasJob {
		return nil // not actually orphaned; it just has not been claimed yet
	}

	if step.StrandedRequeues >= maxStrandedRequeues {
		updated, err := r.Engine.Queries.FailQueuedStep(ctx, dbgen.FailQueuedStepParams{
			ID:        step.ID,
			ErrorCode: idconv.ToPgText("stranded_no_job"),
			ErrorMsg:  idconv.ToPgText("queued with no live River job after repeated re-enqueue attempts"),
		})
		if err != nil {
			return err
		}
		slog.ErrorContext(ctx, "pipeline: failing a step stranded queued with no live job", "step_id", idconv.FromPg(step.ID))
		publishStepEvent(ctx, r.Engine.Pool, updated, true)
		return nil
	}

	tx, err := r.Engine.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := r.Engine.Queries.WithTx(tx)

	updated, err := qtx.IncrementStrandedRequeue(ctx, step.ID)
	if err != nil {
		return err
	}
	if err := r.Engine.enqueueReadySteps(ctx, tx, []dbgen.PipelineStep{updated}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	slog.WarnContext(ctx, "pipeline: re-enqueued a step found queued with no live job", "step_id", idconv.FromPg(step.ID), "stranded_requeues", updated.StrandedRequeues)
	publishStepEvent(ctx, r.Engine.Pool, updated, true)
	return nil
}

// hasLiveRiverJob reports whether any live River job references stepID.
// river_job is River's own migrated schema (see rivermigrate, applied by
// `loomtale migrate`), invisible to sqlc/goose, so this is a hand-written
// query rather than a generated one.
func (e *Engine) hasLiveRiverJob(ctx context.Context, stepID uuid.UUID) (bool, error) {
	var exists bool
	err := e.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM river_job WHERE kind = $1 AND state::text = ANY($2::text[]) AND args -> 'step_ids' ? $3)`,
		JobKind, riverLiveStates, stepID.String(),
	).Scan(&exists)
	return exists, err
}
