package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

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
