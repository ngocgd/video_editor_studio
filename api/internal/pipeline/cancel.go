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

// ErrRetryNotAllowed is returned by RetryStep when the step exists but
// cannot be retried right now: its run is not active (cancelled or
// superseded), or it still has an unmet dependency. Wrapped with a
// specific reason; callers map it to a 409, not a 404.
var ErrRetryNotAllowed = errors.New("pipeline: retry not allowed")

// RetryStep resets a failed or canceled step back to queued and enqueues
// a fresh River job for it. It never revives a step whose run is not
// active, or whose dependencies are not yet satisfied — those requests
// are rejected with ErrRetryNotAllowed rather than silently parking the
// step in "pending". It does not touch dependents: a manual retry
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
		return dbgen.PipelineStep{}, e.explainRetryRejection(ctx, tenantID, stepID)
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

// explainRetryRejection runs a best-effort (non-transactional) follow-up
// read to turn a bare "0 rows" from RetryStep into a specific reason.
// This is purely for a better error message: RetryStep's own WHERE
// clause is still the sole authority on whether the retry actually
// happened.
func (e *Engine) explainRetryRejection(ctx context.Context, tenantID, stepID uuid.UUID) error {
	step, err := e.Queries.GetStepByID(ctx, dbgen.GetStepByIDParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(stepID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if step.Status != StatusFailed && step.Status != StatusCanceled {
		return fmt.Errorf("%w: step is %q, not failed or canceled", ErrRetryNotAllowed, step.Status)
	}
	if step.RemainingDeps > 0 {
		return fmt.Errorf("%w: step has an unmet dependency", ErrRetryNotAllowed)
	}
	run, err := e.Queries.GetRun(ctx, dbgen.GetRunParams{TenantID: idconv.ToPg(tenantID), ID: step.RunID})
	if err == nil && run.Status != "active" {
		return fmt.Errorf("%w: run is %q, not active", ErrRetryNotAllowed, run.Status)
	}
	return ErrRetryNotAllowed
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
// canceled. Returns ErrNotFound both when the run does not exist in this
// tenant and when it is already terminal (done, failed, canceled or
// superseded): cancelling an already-finished run is not a no-op success,
// since a caller relying on the 204 to mean "the run is now stopping"
// would otherwise be misled about a run that, say, already completed.
func (e *Engine) CancelRun(ctx context.Context, tenantID, runID uuid.UUID) error {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := e.Queries.WithTx(tx)

	if _, err := qtx.MarkRunStatusIfNotTerminal(ctx, dbgen.MarkRunStatusIfNotTerminalParams{
		Status: StatusCanceled, TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(runID),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	steps, err := qtx.CancelRunSteps(ctx, dbgen.CancelRunStepsParams{TenantID: idconv.ToPg(tenantID), RunID: idconv.ToPg(runID)})
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
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

// SupersedeRun atomically cancels oldRunID's remaining steps and links it
// to newRunID in one transaction. newRunID must already exist (the
// caller enqueues it first, from a fresh manifest reusing unchanged
// segments per the edit-during-render decision, then calls this): doing
// it in the other order, as a plain CancelRun followed by a separate
// UPDATE setting superseded_by, leaves a window where the old run is
// already "canceled" (observable to any concurrent reader) before the
// replacement exists, and fails outright with a foreign-key violation if
// the caller's own new-run insert has not committed yet.
func (e *Engine) SupersedeRun(ctx context.Context, tenantID, oldRunID, newRunID uuid.UUID) error {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := e.Queries.WithTx(tx)

	if _, err := qtx.SupersedeRunTx(ctx, dbgen.SupersedeRunTxParams{
		NewRunID: idconv.ToPg(newRunID), TenantID: idconv.ToPg(tenantID), OldRunID: idconv.ToPg(oldRunID),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("pipeline: supersede run: %w", err)
	}
	steps, err := qtx.CancelRunSteps(ctx, dbgen.CancelRunStepsParams{TenantID: idconv.ToPg(tenantID), RunID: idconv.ToPg(oldRunID)})
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, s := range steps {
		publishStepEvent(ctx, e.Pool, s, true)
	}
	return nil
}
