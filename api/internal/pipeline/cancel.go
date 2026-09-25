package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// ErrNotFound is returned by Retry/Cancel when the target step or run is
// not in this tenant, or is already in a state the operation does not
// apply to (e.g. cancelling an already-terminal step).
var ErrNotFound = errors.New("pipeline: not found")

// RetryStep resets a failed or canceled step back to queued and enqueues
// a fresh River job for it. It does not touch dependents: a manual retry
// re-runs only the one step, matching the per-step rerun contract.
func (e *Engine) RetryStep(ctx context.Context, tenantID, stepID uuid.UUID) (dbgen.PipelineStep, error) {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return dbgen.PipelineStep{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := e.Queries.WithTx(tx)

	updated, err := qtx.RetryStep(ctx, dbgen.RetryStepParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(stepID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.PipelineStep{}, ErrNotFound
	}
	if err != nil {
		return dbgen.PipelineStep{}, err
	}
	if err := e.enqueueReadySteps(ctx, tx, []dbgen.PipelineStep{updated}); err != nil {
		return dbgen.PipelineStep{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return dbgen.PipelineStep{}, err
	}
	publishStepEvent(ctx, e.Pool, updated, true)
	return updated, nil
}

// CancelStep cancels a single pending/queued/running step. A running
// step's handler notices on its next heartbeat (the CAS on commit/
// heartbeat will find status no longer "running") and stops without
// writing output.
func (e *Engine) CancelStep(ctx context.Context, tenantID, stepID uuid.UUID) (dbgen.PipelineStep, error) {
	updated, err := e.Queries.CancelStep(ctx, dbgen.CancelStepParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(stepID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.PipelineStep{}, ErrNotFound
	}
	if err != nil {
		return dbgen.PipelineStep{}, err
	}
	publishStepEvent(ctx, e.Pool, updated, true)
	return updated, nil
}

// CancelRun cancels every non-terminal step in a run and marks the run
// canceled.
func (e *Engine) CancelRun(ctx context.Context, tenantID, runID uuid.UUID) error {
	steps, err := e.Queries.CancelRunSteps(ctx, dbgen.CancelRunStepsParams{TenantID: idconv.ToPg(tenantID), RunID: idconv.ToPg(runID)})
	if err != nil {
		return err
	}
	if err := e.Queries.MarkRunStatus(ctx, dbgen.MarkRunStatusParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(runID), Status: "canceled"}); err != nil {
		return err
	}
	for _, s := range steps {
		publishStepEvent(ctx, e.Pool, s, true)
	}
	return nil
}

// CancelScope cancels every non-terminal step under (scopeKind,
// scopeID), regardless of which run it belongs to. Domain delete
// services (episodes, scenes, added in later phases) call this inside
// their own transaction before deleting the scope row; it is
// intentionally not itself transactional with a caller-owned delete.
func (e *Engine) CancelScope(ctx context.Context, tenantID uuid.UUID, scopeKind string, scopeID uuid.UUID) error {
	steps, err := e.Queries.CancelScopeSteps(ctx, dbgen.CancelScopeStepsParams{
		TenantID:  idconv.ToPg(tenantID),
		ScopeKind: scopeKind,
		ScopeID:   idconv.ToPg(scopeID),
	})
	if err != nil {
		return err
	}
	for _, s := range steps {
		publishStepEvent(ctx, e.Pool, s, true)
	}
	return nil
}

// SupersedeRun cancels oldRunID's remaining steps, marks it superseded by
// newRunID, and does not touch newRunID (the caller creates it via
// Enqueue separately, from a fresh manifest, reusing unchanged segments
// per the edit-during-render decision).
func (e *Engine) SupersedeRun(ctx context.Context, tenantID, oldRunID, newRunID uuid.UUID) error {
	if err := e.CancelRun(ctx, tenantID, oldRunID); err != nil {
		return fmt.Errorf("pipeline: supersede: cancel old run: %w", err)
	}
	return e.Queries.SupersedeRun(ctx, dbgen.SupersedeRunParams{
		NewRunID: idconv.ToPg(newRunID),
		TenantID: idconv.ToPg(tenantID),
		OldRunID: idconv.ToPg(oldRunID),
	})
}
