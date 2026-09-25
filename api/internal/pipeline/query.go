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

// ListJobs returns a tenant's steps, optionally filtered by status
// and/or queue. Dispatches to one of four sqlc queries, one per filter
// combination, instead of a single query that OR's an optional status
// filter with an optional queue filter: a planner cannot turn that OR
// form into an index scan for any combination (only ever the union of
// both branches, i.e. effectively a full scan regardless of which
// filters are actually set), whereas each of these four maps directly to
// one of the (tenant_id, id) / (tenant_id, status, id) /
// (tenant_id, queue, id) / (tenant_id, queue, status, id) indexes.
func (e *Engine) ListJobs(ctx context.Context, tenantID uuid.UUID, status, queue string, cursor uuid.UUID, limit int32) ([]dbgen.PipelineStep, error) {
	pgTenant := idconv.ToPg(tenantID)
	pgCursor := idconv.ToPg(cursor)
	switch {
	case status != "" && queue != "":
		return e.Queries.ListJobsByStatusAndQueue(ctx, dbgen.ListJobsByStatusAndQueueParams{
			TenantID: pgTenant, StatusFilter: status, QueueFilter: queue, Cursor: pgCursor, PageLimit: limit,
		})
	case status != "":
		return e.Queries.ListJobsByStatus(ctx, dbgen.ListJobsByStatusParams{
			TenantID: pgTenant, StatusFilter: status, Cursor: pgCursor, PageLimit: limit,
		})
	case queue != "":
		return e.Queries.ListJobsByQueue(ctx, dbgen.ListJobsByQueueParams{
			TenantID: pgTenant, QueueFilter: queue, Cursor: pgCursor, PageLimit: limit,
		})
	default:
		return e.Queries.ListJobs(ctx, dbgen.ListJobsParams{TenantID: pgTenant, Cursor: pgCursor, PageLimit: limit})
	}
}

// RunsBelongToTenant checks membership of many run ids at once (used by
// SSE topic authorization, one query instead of one GetRun per topic)
// and returns the subset that actually belong to tenantID.
func (e *Engine) RunsBelongToTenant(ctx context.Context, tenantID uuid.UUID, runIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := e.Queries.GetRunIDsForTenant(ctx, dbgen.GetRunIDsForTenantParams{TenantID: idconv.ToPg(tenantID), Ids: toPgUUIDs(runIDs)})
	if err != nil {
		return nil, err
	}
	found := make(map[uuid.UUID]bool, len(rows))
	for _, id := range rows {
		found[idconv.FromPg(id)] = true
	}
	return found, nil
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
