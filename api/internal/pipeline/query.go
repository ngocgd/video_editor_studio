package pipeline

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// GetRun returns a run, or ErrNotFound if it does not belong to tenantID.
func (e *Engine) GetRun(ctx context.Context, tenantID, runID uuid.UUID) (dbgen.PipelineRun, error) {
	run, err := e.Queries.GetRun(ctx, dbgen.GetRunParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(runID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.PipelineRun{}, ErrNotFound
	}
	return run, err
}

// GetStep returns a step, or ErrNotFound if it does not belong to
// tenantID.
func (e *Engine) GetStep(ctx context.Context, tenantID, stepID uuid.UUID) (dbgen.PipelineStep, error) {
	step, err := e.Queries.GetStepByID(ctx, dbgen.GetStepByIDParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(stepID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.PipelineStep{}, ErrNotFound
	}
	return step, err
}

// ListRunSteps returns steps in a run after cursor, ordered by id.
func (e *Engine) ListRunSteps(ctx context.Context, tenantID, runID, cursor uuid.UUID, limit int32) ([]dbgen.PipelineStep, error) {
	return e.Queries.ListRunSteps(ctx, dbgen.ListRunStepsParams{
		TenantID:  idconv.ToPg(tenantID),
		RunID:     idconv.ToPg(runID),
		Cursor:    idconv.ToPg(cursor),
		PageLimit: limit,
	})
}

// ListJobs returns a tenant's steps, optionally filtered by status/queue.
func (e *Engine) ListJobs(ctx context.Context, tenantID uuid.UUID, status, queue string, cursor uuid.UUID, limit int32) ([]dbgen.PipelineStep, error) {
	return e.Queries.ListJobs(ctx, dbgen.ListJobsParams{
		TenantID:     idconv.ToPg(tenantID),
		StatusFilter: status,
		QueueFilter:  queue,
		Cursor:       idconv.ToPg(cursor),
		PageLimit:    limit,
	})
}

// GpuQueue returns a tenant's own queued gpu steps, ordered the way the
// worker will pick them up (priority, then id).
func (e *Engine) GpuQueue(ctx context.Context, tenantID uuid.UUID, limit int32) ([]dbgen.PipelineStep, error) {
	return e.Queries.ListGpuQueueForTenant(ctx, dbgen.ListGpuQueueForTenantParams{TenantID: idconv.ToPg(tenantID), PageLimit: limit})
}

// RunningGpuStep returns the tenant's own currently running gpu step, if
// any.
func (e *Engine) RunningGpuStep(ctx context.Context, tenantID uuid.UUID) (*dbgen.PipelineStep, error) {
	step, err := e.Queries.GetRunningGpuStepForTenant(ctx, idconv.ToPg(tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &step, nil
}
