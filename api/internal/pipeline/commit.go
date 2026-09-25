package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/obs/scrub"
)

// commitRetries and commitRetryDelay bound how hard commitDoneWithRetry
// tries before giving up and requeuing instead of leaving a step
// stranded in "running": a transient failure here (a deadlock, a
// failover) must never silently redo already-finished work just because
// the one write that would have recorded it as done could not land.
const commitRetries = 3

const commitRetryDelay = 200 * time.Millisecond

// commitDoneWithRetry is commitDone with a small bounded retry: if every
// attempt still fails (and the failure was not the ordinary "reclaimed
// while running" case, which is not an error to retry), it falls back to
// an explicit RequeueStep so the step is claimable again immediately
// instead of waiting for the reconciler's 60s heartbeat sweep to notice
// it never got as far as committing.
func (e *Engine) commitDoneWithRetry(ctx context.Context, step dbgen.PipelineStep, output Output, logAssetID uuid.UUID) error {
	var lastErr error
	for attempt := 0; attempt < commitRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(commitRetryDelay)
		}
		err := e.commitDone(ctx, step, output, logAssetID)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	slog.ErrorContext(ctx, "pipeline: commitDone failed after retries, requeuing instead of stranding the step", "step_id", idconv.FromPg(step.ID), "error", lastErr)
	if err := e.requeueForRetry(ctx, step); err != nil {
		return errors.Join(lastErr, err)
	}
	return lastErr
}

// commitDone commits a step's successful output, fans out to any newly
// ready dependents, and publishes the resulting events, all after the
// commit transaction succeeds.
func (e *Engine) commitDone(ctx context.Context, step dbgen.PipelineStep, output Output, logAssetID uuid.UUID) error {
	if output == nil {
		output = Output{}
	}
	outBytes, err := json.Marshal(output)
	if err != nil {
		return e.commitFailed(ctx, step, "invalid_output", err.Error(), &logAssetID)
	}

	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := e.Queries.WithTx(tx)

	updated, err := qtx.CommitStepDone(ctx, dbgen.CommitStepDoneParams{
		Output:     outBytes,
		LogAssetID: uuidOrNull(logAssetID),
		ID:         step.ID,
		Attempt:    step.Attempt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Reclaimed while we were running: our output is discarded, never
		// written. This is the zombie-writer case the fence exists for.
		return nil
	}
	if err != nil {
		return err
	}

	queuedDependents, err := e.fanIn(ctx, qtx, tx, updated)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	publishStepEvent(ctx, e.Pool, updated, true)
	for _, d := range queuedDependents {
		publishStepEvent(ctx, e.Pool, d, true)
	}
	e.rollupRun(ctx, updated.TenantID, updated.RunID)
	return nil
}

// commitFailed records a terminal failure for step, then cascade-cancels
// any "pending" dependent that can now never run (see
// cascadeCancelPendingDependents) and rolls the run up to done/failed if
// that was its last non-terminal step.
func (e *Engine) commitFailed(ctx context.Context, step dbgen.PipelineStep, code, msg string, logAssetID *uuid.UUID) error {
	logID := uuid.Nil
	if logAssetID != nil {
		logID = *logAssetID
	}
	updated, err := e.Queries.CommitStepFailed(ctx, dbgen.CommitStepFailedParams{
		Status:     StatusFailed,
		ErrorCode:  idconv.ToPgText(code),
		ErrorMsg:   idconv.ToPgText(truncate(scrub.Text(msg), 2000)),
		LogAssetID: uuidOrNull(logID),
		ID:         step.ID,
		Attempt:    step.Attempt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	publishStepEvent(ctx, e.Pool, updated, true)

	for _, canceled := range e.cascadeCancelPendingDependents(ctx, updated.TenantID, updated.ID) {
		publishStepEvent(ctx, e.Pool, canceled, true)
	}
	e.rollupRun(ctx, updated.TenantID, updated.RunID)
	return nil
}

// requeueForRetry moves a running step back to queued (keeping its
// attempt number) so a River retry can reclaim it.
func (e *Engine) requeueForRetry(ctx context.Context, step dbgen.PipelineStep) error {
	updated, err := e.Queries.RequeueStep(ctx, dbgen.RequeueStepParams{ID: step.ID, Attempt: step.Attempt})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	publishStepEvent(ctx, e.Pool, updated, true)
	return nil
}

// handleGPUOOM implements the gpu_oom policy: a full residency unload,
// requeue the step so it is claimable again, and exactly one retry,
// tracked by the step's own durable gpu_oom_count (not River's attempt
// count, which a transient failure on an earlier attempt would otherwise
// miscount as "already OOM'd once"). It also stops the rest of the
// current chunk (see errStopChunk): the GPU has no model loaded any
// more, so running the remaining claimed... rather, not-yet-claimed
// steps immediately would just OOM again. A gpu_oom classification
// reaching here with no Residency configured (i.e. off the gpu queue,
// which should never happen) degrades to a permanent failure rather than
// panicking.
func (e *Engine) handleGPUOOM(ctx context.Context, step dbgen.PipelineStep, runErr error, logAssetID *uuid.UUID, opts DispatchOpts) error {
	if opts.Residency == nil {
		return e.commitFailed(ctx, step, "gpu_oom", runErr.Error(), logAssetID)
	}
	if unloadErr := opts.Residency.UnloadAll(ctx); unloadErr != nil {
		slog.ErrorContext(ctx, "pipeline: failed to unload GPU models after OOM", "error", unloadErr)
	}

	counted, err := e.Queries.IncrementGpuOomCount(ctx, dbgen.IncrementGpuOomCountParams{ID: step.ID, Attempt: step.Attempt})
	if errors.Is(err, pgx.ErrNoRows) {
		return errStopChunk // reclaimed; nothing more to do
	}
	if err != nil {
		return errors.Join(runErr, err, errStopChunk)
	}
	if counted.GpuOomCount > 1 {
		if err := e.commitFailed(ctx, step, "gpu_oom", runErr.Error(), logAssetID); err != nil {
			return errors.Join(err, errStopChunk)
		}
		return errStopChunk
	}
	if err := e.requeueForRetry(ctx, step); err != nil {
		return errors.Join(runErr, err, errStopChunk)
	}
	return errors.Join(runErr, errStopChunk)
}

func uuidOrNull(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return idconv.ToPgPtr(nil)
	}
	return idconv.ToPg(id)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
