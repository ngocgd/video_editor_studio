package pipelineapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// logURLTTL is how long the presigned log download URL stays valid;
// short-lived since a log is small and re-requested cheaply.
const logURLTTL = 10 * time.Minute

// RetryStep implements gen.StrictServerInterface. RBAC guarantees an
// editor+ role.
func (h *PipelineAPI) RetryStep(ctx context.Context, req gen.RetryStepRequestObject) (gen.RetryStepResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	step, err := h.Engine.RetryStep(ctx, info.ID, req.Id)
	if errors.Is(err, pipeline.ErrNotFound) {
		detail := "step not found, or not in a retryable state"
		return gen.RetryStep404ApplicationProblemPlusJSONResponse{Title: "not found", Status: http.StatusNotFound, Detail: &detail}, nil
	}
	if err != nil {
		return nil, err
	}
	recordJobAudit(ctx, h.Engine, "step_retried", "pipeline_step", req.Id)
	return gen.RetryStep200JSONResponse(toStepDTO(step)), nil
}

// CancelStep implements gen.StrictServerInterface. RBAC guarantees an
// editor+ role.
func (h *PipelineAPI) CancelStep(ctx context.Context, req gen.CancelStepRequestObject) (gen.CancelStepResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	step, err := h.Engine.CancelStep(ctx, info.ID, req.Id)
	if errors.Is(err, pipeline.ErrNotFound) {
		detail := "step not found, or already terminal"
		return gen.CancelStep404ApplicationProblemPlusJSONResponse{Title: "not found", Status: http.StatusNotFound, Detail: &detail}, nil
	}
	if err != nil {
		return nil, err
	}
	recordJobAudit(ctx, h.Engine, "step_canceled", "pipeline_step", req.Id)
	return gen.CancelStep200JSONResponse(toStepDTO(step)), nil
}

// ListJobs implements gen.StrictServerInterface.
func (h *PipelineAPI) ListJobs(ctx context.Context, req gen.ListJobsRequestObject) (gen.ListJobsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	status, queue, cursorStr := "", "", ""
	if req.Params.Status != nil {
		status = *req.Params.Status
	}
	if req.Params.Queue != nil {
		queue = *req.Params.Queue
	}
	if req.Params.Cursor != nil {
		cursorStr = *req.Params.Cursor
	}
	cursor, err := httpx.DecodeCursor(cursorStr)
	if err != nil {
		cursor = uuid.Nil
	}
	limit := httpx.PageLimit(req.Params.Limit)

	steps, err := h.Engine.ListJobs(ctx, info.ID, status, queue, cursor, limit)
	if err != nil {
		return nil, err
	}
	return gen.ListJobs200JSONResponse(toStepListDTO(steps, limit)), nil
}

// GetStepLog implements gen.StrictServerInterface. RBAC guarantees an
// editor+ role (owner satisfies editor+, matching "editor or owner
// only").
func (h *PipelineAPI) GetStepLog(ctx context.Context, req gen.GetStepLogRequestObject) (gen.GetStepLogResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	step, err := h.Engine.GetStep(ctx, info.ID, req.Id)
	if errors.Is(err, pipeline.ErrNotFound) {
		detail := "step not found"
		return gen.GetStepLog404ApplicationProblemPlusJSONResponse{Title: "not found", Status: http.StatusNotFound, Detail: &detail}, nil
	}
	if err != nil {
		return nil, err
	}
	if !step.LogAssetID.Valid {
		detail := "this step has no log yet"
		return gen.GetStepLog404ApplicationProblemPlusJSONResponse{Title: "not found", Status: http.StatusNotFound, Detail: &detail}, nil
	}

	asset, err := h.Engine.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{
		TenantID: idconv.ToPg(info.ID),
		ID:       step.LogAssetID,
	})
	if err != nil {
		return nil, err
	}
	url, err := h.Storage.PresignGet(ctx, asset.StorageKey, asset.StorageVersionID.String, logURLTTL)
	if err != nil {
		return nil, err
	}
	return gen.GetStepLog200JSONResponse{Url: url, ExpiresAt: nowUTC().Add(logURLTTL)}, nil
}
