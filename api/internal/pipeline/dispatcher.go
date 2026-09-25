package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

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

// missingHandlerSnooze is how long Dispatch waits before a River retry
// when a chunk contains a step kind this process has no handler for
// (an old binary mid-rollout, GPU disabled on this worker, or — today —
// no phase past 3 having registered anything yet). Snoozing never
// consumes a River attempt, so a step can wait indefinitely for a
// capable worker instead of being destroyed by an incapable one.
const missingHandlerSnooze = 30 * time.Second

// DispatchOpts carries the parts of dispatch that only apply to a GPU
// job: Residency is nil for every non-gpu queue, in which case a
// gpu_oom-classified error (which should never happen off the gpu queue)
// degrades to a permanent failure instead of panicking on a nil
// interface. RiverAttempt/RiverMaxAttempts are River's own attempt
// bookkeeping (distinct from the step's DB attempt), used to fail a step
// outright on its last transient retry instead of requeuing it forever.
type DispatchOpts struct {
	Sink             logSink
	Residency        ModelResidency
	RiverAttempt     int
	RiverMaxAttempts int
}

// errStopChunk signals Dispatch to stop claiming further ids in this
// call (used after a gpu_oom: the GPU has no model loaded any more, so
// running the rest of the chunk would just OOM again immediately). The
// ids not yet claimed are simply left "queued" for a later job.
var errStopChunk = errors.New("pipeline: stop working the rest of this chunk")

// Dispatch claims and runs each id in turn, one at a time (not all up
// front): claiming the whole chunk before working any of it would mark
// every step "running" immediately, but only the one actually being
// worked has a live heartbeat goroutine, so the rest go heartbeat-stale
// and get reclaimed by the reconciler while this same call is still
// (uselessly) about to work them. It returns a non-nil error only when
// the whole River job should be retried: a transient or first-time
// gpu_oom failure on at least one claimed step, or a missing handler for
// an unclaimed one (which snoozes instead of failing).
func (e *Engine) Dispatch(ctx context.Context, jobID int64, ids []uuid.UUID, opts DispatchOpts) error {
	var retryErr error
	for _, id := range ids {
		peeked, err := e.Queries.PeekSteps(ctx, toPgUUIDs([]uuid.UUID{id}))
		if err != nil {
			return fmt.Errorf("pipeline: peek: %w", err)
		}
		if len(peeked) == 0 {
			continue // gone: canceled, or the id never existed
		}
		if _, ok := e.Registry.Lookup(peeked[0].Kind); !ok {
			// A worker that cannot run this kind must never claim (and
			// thereby destroy) the step; leave it queued for one that can.
			return river.JobSnooze(missingHandlerSnooze)
		}

		claimed, err := Claim(ctx, e.Queries, jobID, []uuid.UUID{id})
		if err != nil {
			return fmt.Errorf("pipeline: claim: %w", err)
		}
		if len(claimed) == 0 {
			continue // reclaimed by someone else, or already terminal
		}

		if err := e.runOne(ctx, claimed[0], opts); err != nil {
			if errors.Is(err, errStopChunk) {
				return err
			}
			if retryErr == nil {
				retryErr = err
			}
		}
	}
	return retryErr
}

// runOne runs a single claimed step and commits its result. The returned
// error, if any, signals "retry this River job" (possibly wrapped in
// errStopChunk); it is never returned for a permanent failure, which is
// terminal by design.
func (e *Engine) runOne(ctx context.Context, step dbgen.PipelineStep, opts DispatchOpts) error {
	handler, ok := e.Registry.Lookup(step.Kind)
	if !ok {
		// Dispatch already checked this before claiming, but the
		// registry could change between the peek and the claim in a
		// pathological case; fail closed rather than run nothing and
		// silently drop the step.
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
		return e.commitDoneWithRetry(commitCtx, step, output, logAssetID)
	}

	class, code := Classify(runErr)
	switch class {
	case ClassPermanent:
		return e.commitFailed(commitCtx, step, code, runErr.Error(), &logAssetID)
	case ClassGPUOOM:
		return e.handleGPUOOM(commitCtx, step, runErr, &logAssetID, opts)
	default: // ClassTransient
		if opts.RiverMaxAttempts > 0 && opts.RiverAttempt >= opts.RiverMaxAttempts {
			// This is the last attempt River will ever give this job:
			// requeuing would strand the step in "queued" forever (no
			// future job will exist to reclaim it). Fail it outright so
			// it stops holding quota and is visible as an actionable
			// error instead.
			return e.commitFailed(commitCtx, step, "exhausted_retries", runErr.Error(), &logAssetID)
		}
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
