package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

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
	return nil
}

// commitFailed records a terminal failure for step.
func (e *Engine) commitFailed(ctx context.Context, step dbgen.PipelineStep, code, msg string, logAssetID *uuid.UUID) error {
	logID := uuid.Nil
	if logAssetID != nil {
		logID = *logAssetID
	}
	updated, err := e.Queries.CommitStepFailed(ctx, dbgen.CommitStepFailedParams{
		Status:     StatusFailed,
		ErrorCode:  idconv.ToPgText(code),
		ErrorMsg:   idconv.ToPgText(truncate(msg, 2000)),
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
// requeue the step so it is claimable again, and exactly one retry.
// opts.RiverAttempt is River's own attempt counter (distinct from the
// step's DB attempt), which bounds the retry: a second OOM for the same
// River job is permanent. A gpu_oom classification reaching here with no
// Residency configured (i.e. off the gpu queue, which should never
// happen) degrades to a permanent failure rather than panicking.
func (e *Engine) handleGPUOOM(ctx context.Context, step dbgen.PipelineStep, runErr error, logAssetID *uuid.UUID, opts DispatchOpts) error {
	if opts.Residency == nil {
		return e.commitFailed(ctx, step, "gpu_oom", runErr.Error(), logAssetID)
	}
	if unloadErr := opts.Residency.UnloadAll(ctx); unloadErr != nil {
		slog.ErrorContext(ctx, "pipeline: failed to unload GPU models after OOM", "error", unloadErr)
	}
	if opts.RiverAttempt > 1 {
		return e.commitFailed(ctx, step, "gpu_oom", runErr.Error(), logAssetID)
	}
	if err := e.requeueForRetry(ctx, step); err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
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
