package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// heartbeatInterval is how often a running step's heartbeat_at is
// refreshed; the reconciler resets anything older than
// heartbeatStaleAfter back to queued.
const heartbeatInterval = 10 * time.Second

// heartbeatStaleAfter must stay comfortably above heartbeatInterval so a
// single missed tick under load does not cause a spurious reclaim.
const heartbeatStaleAfter = 60 * time.Second

// DispatchOpts carries the parts of dispatch that only apply to a GPU
// job: Residency is nil for every non-gpu queue, in which case a
// gpu_oom-classified error (which should never happen off the gpu queue)
// degrades to a permanent failure instead of panicking on a nil
// interface. RiverAttempt is River's own attempt counter (distinct from
// the step's DB attempt), used to bound gpu_oom to exactly one retry.
type DispatchOpts struct {
	Sink         logSink
	Residency    ModelResidency
	RiverAttempt int
}

// Dispatch claims every step in ids under jobID and runs each one to
// completion (or a classified failure) through its registered handler.
// It returns a non-nil error only when the whole River job should be
// retried: a transient or first-time gpu_oom failure on at least one
// claimed step. A permanent failure is recorded on the step itself and
// does not fail the job.
func (e *Engine) Dispatch(ctx context.Context, jobID int64, ids []uuid.UUID, opts DispatchOpts) error {
	claimed, err := Claim(ctx, e.Queries, jobID, ids)
	if err != nil {
		return fmt.Errorf("pipeline: claim: %w", err)
	}
	if len(claimed) == 0 {
		// Every id was already handled (done/failed/canceled/reclaimed by
		// someone else) or never existed; nothing to do. This is the
		// expected outcome for a duplicate River rescue firing alongside
		// the reconciler.
		return nil
	}

	var retryErr error
	for _, step := range claimed {
		if err := e.runOne(ctx, step, opts); err != nil {
			if retryErr == nil {
				retryErr = err
			}
		}
	}
	return retryErr
}

// runOne runs a single claimed step and commits its result. The returned
// error, if any, signals "retry this River job"; it is never returned
// for a permanent failure, which is terminal by design.
func (e *Engine) runOne(ctx context.Context, step dbgen.PipelineStep, opts DispatchOpts) error {
	handler, ok := e.Registry.Lookup(step.Kind)
	if !ok {
		_ = e.commitFailed(ctx, step, "no_handler", fmt.Sprintf("no handler registered for step kind %q", step.Kind), nil)
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	stop := e.startHeartbeat(runCtx, cancel, step)
	defer stop()

	sc := newStepContext(runCtx, e.Queries, e.Pool, step, opts.Sink)
	output, runErr := handler.Run(runCtx, sc)

	// Every write from here on (log flush, commit, requeue) is bookkeeping
	// that must complete even if ctx itself was cancelled mid-run (a
	// client disconnect, the GPU watchdog losing its lock connection): a
	// cancelled context must never be the reason a step is left stranded
	// in "running" until the reconciler's 60s sweep finds it.
	commitCtx, cancelCommit := detachedContext()
	defer cancelCommit()
	logAssetID := sc.flushLog(commitCtx)

	if runErr == nil {
		return e.commitDone(commitCtx, step, output, logAssetID)
	}

	class, code := Classify(runErr)
	switch class {
	case ClassPermanent:
		return e.commitFailed(commitCtx, step, code, runErr.Error(), &logAssetID)
	case ClassGPUOOM:
		return e.handleGPUOOM(commitCtx, step, runErr, &logAssetID, opts)
	default: // ClassTransient
		if err := e.requeueForRetry(commitCtx, step); err != nil {
			slog.ErrorContext(commitCtx, "pipeline: failed to requeue step after transient error", "step_id", idconv.FromPg(step.ID), "error", err)
		}
		return runErr
	}
}

// commitTimeout bounds the detached context used for post-run
// bookkeeping writes (see detachedContext): generous enough for a normal
// commit, short enough that a genuinely dead database does not hang a
// worker goroutine forever.
const commitTimeout = 15 * time.Second

// detachedContext returns a fresh context, not derived from the one a
// step ran under, so a step's own cancellation (client disconnect, lock
// loss, shutdown) can never prevent the DB write that records what
// happened to it.
func detachedContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), commitTimeout)
}

// startHeartbeat launches a goroutine that refreshes step's heartbeat
// every heartbeatInterval and cancels cancel (stopping the handler) the
// moment a heartbeat write reports zero rows affected, meaning the
// reconciler already reclaimed this step out from under us. It returns a
// stop function the caller must call when the step finishes on its own.
func (e *Engine) startHeartbeat(ctx context.Context, cancel context.CancelFunc, step dbgen.PipelineStep) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				alive, err := Heartbeat(context.Background(), e.Queries, idconv.FromPg(step.ID), step.Attempt)
				if err != nil {
					slog.WarnContext(ctx, "pipeline: heartbeat write failed", "step_id", idconv.FromPg(step.ID), "error", err)
					continue
				}
				if !alive {
					slog.WarnContext(ctx, "pipeline: step was reclaimed, cancelling", "step_id", idconv.FromPg(step.ID))
					cancel()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

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
