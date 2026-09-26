package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// gpuSlotLockKey is the fixed Postgres advisory lock key guarding the
// single physical GPU. It is an arbitrary constant, not derived from any
// table id, so every worker process agrees on it without a lookup.
const gpuSlotLockKey int64 = 0x6c745f677075 // "lt_gpu" packed into an int64

// GPUSlotLockKey exposes the GPU advisory lock key to operator tools
// (the benchmark CLI) that drive the GPU directly: holding the same
// session lock keeps every gpu-queue step off the GPU while they run.
const GPUSlotLockKey = gpuSlotLockKey

// gpuLockWatchdogInterval controls how often the dedicated lock
// connection is pinged; losing it (network blip, connection killed) must
// cancel the running job's context quickly enough that a second worker
// process picking up the lock never overlaps for long.
const gpuLockWatchdogInterval = 5 * time.Second

// gpuLockPingTimeout bounds a single watchdog ping: without a timeout, a
// half-open TCP connection (the remote end vanished without a clean
// close) never returns an error and never trips the watchdog, exactly
// the failure mode the watchdog exists to catch.
const gpuLockPingTimeout = 2 * time.Second

// maxResidentPreferenceSnoozes bounds how many times a job defers to let
// a same-priority queued gpu step targeting the currently resident model
// run first, so preferring residency can never starve a step outright.
const maxResidentPreferenceSnoozes = 3

// GPUExecutor owns the single-GPU advisory lock, the lock-connection
// watchdog, and the resident-model preference check. It is only used for
// jobs on the "gpu" queue.
type GPUExecutor struct {
	Engine          *Engine
	Residency       ModelResidency
	RenderReserveMB int64

	mu      sync.Mutex
	snoozes map[int64]int // River job id -> resident-preference snooze count
}

// NewGPUExecutor builds a GPUExecutor. residency must be a real
// implementation (phase 4) when WORKER_GPU=true; NoopResidency otherwise,
// in which case the gpu queue must never be enabled.
func NewGPUExecutor(e *Engine, residency ModelResidency, renderReserveMB int64) *GPUExecutor {
	return &GPUExecutor{Engine: e, Residency: residency, RenderReserveMB: renderReserveMB, snoozes: make(map[int64]int)}
}

// Run is called for every job on the "gpu" queue. It tries the advisory
// lock; on failure it snoozes briefly rather than occupying a worker slot
// spinning. On success it holds the lock (with a watchdog cancelling ctx
// if the connection dies), applies the resident-model preference, ensures
// the target model is loaded, and only then claims and dispatches.
func (g *GPUExecutor) Run(ctx context.Context, jobID int64, riverAttempt, riverMaxAttempts int, ids []uuid.UUID, sink logSink) error {
	conn, err := g.Engine.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("pipeline: acquire GPU lock connection: %w", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			conn.Release()
		}
	}
	defer release()

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", gpuSlotLockKey).Scan(&locked); err != nil {
		return fmt.Errorf("pipeline: try GPU advisory lock: %w", err)
	}
	if !locked {
		g.clearSnoozeCount(jobID)
		return river.JobSnooze(5 * time.Second)
	}

	watchdogCtx, cancelWatchdog := context.WithCancel(ctx)
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		g.watchLockConnection(watchdogCtx, conn, cancelWatchdog)
	}()
	defer func() {
		// Cancel and, critically, wait for the watchdog goroutine to
		// actually exit before touching conn again: a Ping still in
		// flight on the same *pgxpool.Conn makes the unlock query fail
		// with "conn busy", and unlocking is meaningless if the
		// connection is about to be handed back to the pool anyway.
		cancelWatchdog()
		<-watchdogDone

		unlockCtx, cancel := context.WithTimeout(context.Background(), gpuLockPingTimeout)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", gpuSlotLockKey); err != nil {
			// The unlock failed: the connection may still hold the
			// (re-entrant, session-scoped) advisory lock. Handing it back
			// to the pool via Release would let some future checkout
			// silently inherit a connection that never actually released
			// the GPU slot, locking every other process out until pgxpool
			// happens to recycle it. Hijack takes the raw connection out
			// of the pool's accounting entirely (Release must never also
			// run on it after this), then it is closed outright so the
			// pool opens a fresh one next time instead of reusing it.
			slog.ErrorContext(ctx, "pipeline: failed to release the GPU advisory lock, closing the connection instead of returning it to the pool", "error", err)
			released = true
			raw := conn.Hijack()
			if closeErr := raw.Close(context.Background()); closeErr != nil {
				slog.WarnContext(ctx, "pipeline: failed to close the hijacked GPU lock connection", "error", closeErr)
			}
		}
	}()

	if snooze, err := g.preferResident(watchdogCtx, jobID, ids); err != nil {
		return err
	} else if snooze {
		return river.JobSnooze(2 * time.Second)
	}
	g.clearSnoozeCount(jobID)

	if err := g.ensureModel(watchdogCtx, ids); err != nil {
		return err
	}

	return g.Engine.Dispatch(watchdogCtx, jobID, ids, DispatchOpts{
		Sink: sink, Residency: g.Residency, RiverAttempt: riverAttempt, RiverMaxAttempts: riverMaxAttempts,
	})
}

// pinger is the minimal interface the watchdog needs from the dedicated
// lock connection; *pgxpool.Conn satisfies it.
type pinger interface {
	Ping(ctx context.Context) error
}

// watchLockConnection pings conn every gpuLockWatchdogInterval (each
// ping bounded by gpuLockPingTimeout) and cancels cancel the moment a
// ping fails, so a job never keeps running after this worker silently
// lost the advisory lock (e.g. the connection itself was dropped, which
// also releases the lock server-side). Returns as soon as ctx is done,
// so the caller can safely wait on this returning before reusing conn.
func (g *GPUExecutor) watchLockConnection(ctx context.Context, conn pinger, cancel context.CancelFunc) {
	ticker := time.NewTicker(gpuLockWatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancelPing := context.WithTimeout(ctx, gpuLockPingTimeout)
			err := conn.Ping(pingCtx)
			cancelPing()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

// preferResident reports whether this job should snooze briefly to let a
// same-priority queued gpu step targeting the currently resident model
// run first, avoiding a model swap for a job that would otherwise be
// followed immediately by a swap back.
func (g *GPUExecutor) preferResident(ctx context.Context, jobID int64, ids []uuid.UUID) (bool, error) {
	current := g.Residency.Current()
	if current == nil {
		return false, nil
	}
	if g.snoozeCount(jobID) >= maxResidentPreferenceSnoozes {
		return false, nil
	}

	steps, err := g.Engine.Queries.PeekSteps(ctx, toPgUUIDs(ids))
	if err != nil || len(steps) == 0 {
		return false, err
	}
	target := steps[0]
	residentRef := current.Backend + ":" + current.Model
	if target.ProviderRef == residentRef {
		return false, nil // already targeting the resident model
	}

	waiting, err := g.Engine.Queries.CountQueuedGpuStepsForModel(ctx, dbgen.CountQueuedGpuStepsForModelParams{
		Priority:    target.Priority,
		ProviderRef: residentRef,
	})
	if err != nil {
		return false, err
	}
	if waiting == 0 {
		return false, nil
	}
	g.incrementSnoozeCount(jobID)
	return true, nil
}

// ensureModel waits for the target step's model to be resident, using
// PeekSteps rather than the claimed rows because this runs before claim.
// Every step in a chunk shares the same provider_ref (enqueueReadySteps
// groups by (queue, kind, provider_ref)), so steps[0]'s model is the
// whole chunk's model, not just an arbitrary representative.
func (g *GPUExecutor) ensureModel(ctx context.Context, ids []uuid.UUID) error {
	steps, err := g.Engine.Queries.PeekSteps(ctx, toPgUUIDs(ids))
	if err != nil || len(steps) == 0 {
		return err
	}
	handler, ok := g.Engine.Registry.Lookup(steps[0].Kind)
	if !ok {
		return nil // Dispatch/runOne will record the no-handler failure itself
	}
	ref := StepRef{
		ID:        idconv.FromPg(steps[0].ID),
		TenantID:  idconv.FromPg(steps[0].TenantID),
		RunID:     idconv.FromPg(steps[0].RunID),
		ScopeKind: steps[0].ScopeKind,
		ScopeID:   idconv.FromPg(steps[0].ScopeID),
		Kind:      steps[0].Kind,
	}
	model, err := handler.ModelRef(ctx, ref)
	if err != nil || model == nil {
		return err
	}
	return g.Residency.Ensure(ctx, *model)
}

func (g *GPUExecutor) snoozeCount(jobID int64) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.snoozes[jobID]
}

func (g *GPUExecutor) incrementSnoozeCount(jobID int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.snoozes[jobID]++
}

// clearSnoozeCount drops jobID's entry. Called both when a job stops
// needing to defer (it got the lock and either proceeded or lost the
// resident-preference race enough times to stop deferring) and when it
// never got the lock at all, so a job id that only ever snoozed on the
// advisory lock itself (never reaching preferResident) cannot leak an
// entry that nothing else will ever clear.
func (g *GPUExecutor) clearSnoozeCount(jobID int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.snoozes, jobID)
}

// RenderAdmitted reports whether a render.* job may start now: it must
// see at least RenderReserveMB free on the GPU, otherwise the caller
// should snooze (see worker.go).
func RenderAdmitted(ctx context.Context, probe GpuProbe, renderReserveMB int64) (bool, error) {
	snap, err := probe.Snapshot(ctx)
	if err != nil {
		return false, err
	}
	return snap.FreeMB >= renderReserveMB, nil
}
