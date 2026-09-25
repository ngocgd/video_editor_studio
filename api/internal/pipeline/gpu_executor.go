package pipeline

import (
	"context"
	"fmt"
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

// gpuLockWatchdogInterval controls how often the dedicated lock
// connection is pinged; losing it (network blip, connection killed) must
// cancel the running job's context quickly enough that a second worker
// process picking up the lock never overlaps for long.
const gpuLockWatchdogInterval = 5 * time.Second

// maxResidentPreferenceSnoozes bounds how many times a job defers to let
// a same-priority queued step for the currently resident model run
// first, so preferring residency can never starve a step outright.
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
func (g *GPUExecutor) Run(ctx context.Context, jobID int64, riverAttempt int, ids []uuid.UUID, sink logSink) error {
	conn, err := g.Engine.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("pipeline: acquire GPU lock connection: %w", err)
	}
	defer conn.Release()

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", gpuSlotLockKey).Scan(&locked); err != nil {
		return fmt.Errorf("pipeline: try GPU advisory lock: %w", err)
	}
	if !locked {
		return river.JobSnooze(5 * time.Second)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", gpuSlotLockKey)
	}()

	watchdogCtx, cancelWatchdog := context.WithCancel(ctx)
	defer cancelWatchdog()
	go g.watchLockConnection(watchdogCtx, conn, cancelWatchdog)

	if snooze, err := g.preferResident(watchdogCtx, jobID, ids); err != nil {
		return err
	} else if snooze {
		return river.JobSnooze(2 * time.Second)
	}
	g.clearSnoozeCount(jobID)

	if err := g.ensureModel(watchdogCtx, ids); err != nil {
		return err
	}

	return g.Engine.Dispatch(watchdogCtx, jobID, ids, DispatchOpts{Sink: sink, Residency: g.Residency, RiverAttempt: riverAttempt})
}

// pinger is the minimal interface the watchdog needs from the dedicated
// lock connection; *pgxpool.Conn satisfies it.
type pinger interface {
	Ping(ctx context.Context) error
}

// watchLockConnection pings conn every gpuLockWatchdogInterval and
// cancels cancel the moment the ping fails, so a job never keeps running
// after this worker silently lost the advisory lock (e.g. the connection
// itself was dropped, which also releases the lock server-side).
func (g *GPUExecutor) watchLockConnection(ctx context.Context, conn pinger, cancel context.CancelFunc) {
	ticker := time.NewTicker(gpuLockWatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.Ping(ctx); err != nil {
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
func (g *GPUExecutor) ensureModel(ctx context.Context, ids []uuid.UUID) error {
	steps, err := g.Engine.Queries.PeekSteps(ctx, toPgUUIDs(ids))
	if err != nil || len(steps) == 0 {
		return err
	}
	handler, ok := g.Engine.Registry.Lookup(steps[0].Kind)
	if !ok {
		return nil // runOne will record the no-handler failure itself
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
