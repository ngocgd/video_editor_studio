package pipeline

import (
	"context"
	"log/slog"
	"time"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// reconcileInterval is how often the reconciler runs after its initial
// boot-time pass.
const reconcileInterval = 60 * time.Second

// reconcileBatchLimit bounds every reconciler sweep query so it never
// holds one giant transaction over an unbounded row set; the reconciler
// loops a batch at a time until a pass returns fewer rows than the
// limit.
const reconcileBatchLimit = 500

// Reconciler is the crash-resume safety net: it resets steps whose
// heartbeat went stale (the worker that claimed them died or was
// killed), sweeps any pending step whose dependencies are already
// satisfied but that was never enqueued (a fan-in gap), sweeps any
// queued step with no live River job (a step that fell out of River's
// own bookkeeping), and inserts a fresh River job for everything it just
// moved to queued.
type Reconciler struct {
	Engine *Engine
}

// Run executes one pass immediately (the boot-time pass) and then one
// pass every reconcileInterval until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	r.RunOnce(ctx)
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce executes a single reconcile pass. Exported so tests (and a
// future manual "reconcile now" operator hook) can drive one pass
// deterministically instead of waiting on Run's own ticker.
func (r *Reconciler) RunOnce(ctx context.Context) {
	if err := r.resetStaleHeartbeats(ctx); err != nil {
		slog.ErrorContext(ctx, "pipeline: reconciler failed to reset stale heartbeats", "error", err)
	}
	if err := r.sweepReady(ctx); err != nil {
		slog.ErrorContext(ctx, "pipeline: reconciler failed to sweep ready steps", "error", err)
	}
	if err := r.sweepOrphanedQueued(ctx); err != nil {
		slog.ErrorContext(ctx, "pipeline: reconciler failed to sweep orphaned queued steps", "error", err)
	}
}

// resetStaleHeartbeats moves every "running" step whose heartbeat is
// older than heartbeatStaleAfter back to "queued" and enqueues a fresh
// River job for it, one bounded batch and transaction at a time, so a
// step is never left queued with nothing that will ever claim it.
func (r *Reconciler) resetStaleHeartbeats(ctx context.Context) error {
	cutoff := idconv.ToPgTimestamptz(time.Now().Add(-heartbeatStaleAfter))
	for {
		tx, err := r.Engine.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		qtx := r.Engine.Queries.WithTx(tx)

		reset, err := qtx.ResetStaleHeartbeatsBatch(ctx, dbgen.ResetStaleHeartbeatsBatchParams{Cutoff: cutoff, PageLimit: reconcileBatchLimit})
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if len(reset) > 0 {
			if err := r.Engine.enqueueReadySteps(ctx, tx, reset); err != nil {
				_ = tx.Rollback(ctx)
				return err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		for _, s := range reset {
			slog.WarnContext(ctx, "pipeline: reclaimed a step with a stale heartbeat", "step_id", idconv.FromPg(s.ID), "attempt", s.Attempt)
			publishStepEvent(ctx, r.Engine.Pool, s, true)
		}
		if len(reset) < reconcileBatchLimit {
			return nil
		}
	}
}

// sweepReady moves every "pending" step whose remaining_deps already
// reached zero to "queued" and enqueues it. This closes the gap where a
// dependency completed and fan-in's own enqueue failed after its DB
// commit (e.g. the process crashed between the two), or where
// remaining_deps was recomputed by MarkStaleDependents outside the
// normal fan-in path.
func (r *Reconciler) sweepReady(ctx context.Context) error {
	for {
		tx, err := r.Engine.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		qtx := r.Engine.Queries.WithTx(tx)

		ready, err := qtx.ReadySweepBatch(ctx, reconcileBatchLimit)
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if len(ready) > 0 {
			if err := r.Engine.enqueueReadySteps(ctx, tx, ready); err != nil {
				_ = tx.Rollback(ctx)
				return err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		for _, s := range ready {
			publishStepEvent(ctx, r.Engine.Pool, s, true)
		}
		if len(ready) < reconcileBatchLimit {
			return nil
		}
	}
}
