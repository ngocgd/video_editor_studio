package pipelineapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// CreateRun implements gen.StrictServerInterface. RBAC guarantees an
// editor+ role in a resolved tenant for this route.
func (h *PipelineAPI) CreateRun(ctx context.Context, req gen.CreateRunRequestObject) (gen.CreateRunResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	body := req.Body

	runID := idconv.NewV7()
	if body.Id != nil {
		runID = uuid.UUID(*body.Id)
	}

	spec := pipeline.RunSpec{
		ID:        runID,
		ScopeKind: body.ScopeKind,
		ScopeID:   body.ScopeId,
		Kind:      body.Kind,
		Steps:     toModelRefSpecs(body.Steps),
	}

	if _, err := h.Engine.Enqueue(ctx, info.ID, spec); err != nil {
		switch {
		case errors.Is(err, pipeline.ErrQuotaExceeded):
			detail := err.Error()
			return gen.CreateRun429ApplicationProblemPlusJSONResponse{Title: "quota exceeded", Status: http.StatusTooManyRequests, Detail: &detail}, nil
		case errors.Is(err, pipeline.ErrAdmissionDenied):
			detail := err.Error()
			return gen.CreateRun507ApplicationProblemPlusJSONResponse{Title: "admission denied", Status: http.StatusInsufficientStorage, Detail: &detail}, nil
		default:
			// Anything else (a malformed step spec referencing an
			// unregistered kind, a bad dependency graph) is the caller's
			// mistake, not a server fault, so it is surfaced as 400
			// rather than falling through to the generic 500 handler.
			detail := err.Error()
			return gen.CreateRun400ApplicationProblemPlusJSONResponse{Title: "invalid run", Status: http.StatusBadRequest, Detail: &detail}, nil
		}
	}

	run, err := h.Engine.GetRun(ctx, info.ID, runID)
	if err != nil {
		return nil, err
	}
	return gen.CreateRun201JSONResponse(toRunDTO(run)), nil
}

// GetRun implements gen.StrictServerInterface.
func (h *PipelineAPI) GetRun(ctx context.Context, req gen.GetRunRequestObject) (gen.GetRunResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	run, err := h.Engine.GetRun(ctx, info.ID, req.Id)
	if errors.Is(err, pipeline.ErrNotFound) {
		detail := "run not found"
		return gen.GetRun404ApplicationProblemPlusJSONResponse{Title: "not found", Status: http.StatusNotFound, Detail: &detail}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetRun200JSONResponse(toRunDTO(run)), nil
}

// ListRunSteps implements gen.StrictServerInterface.
func (h *PipelineAPI) ListRunSteps(ctx context.Context, req gen.ListRunStepsRequestObject) (gen.ListRunStepsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	cursorStr := ""
	if req.Params.Cursor != nil {
		cursorStr = *req.Params.Cursor
	}
	cursor, err := httpx.DecodeCursor(cursorStr)
	if err != nil {
		cursor = uuid.Nil
	}
	limit := httpx.PageLimit(req.Params.Limit)

	steps, err := h.Engine.ListRunSteps(ctx, info.ID, req.Id, cursor, limit)
	if err != nil {
		return nil, err
	}
	return gen.ListRunSteps200JSONResponse(toStepListDTO(steps, limit)), nil
}

// CancelRun implements gen.StrictServerInterface.
func (h *PipelineAPI) CancelRun(ctx context.Context, req gen.CancelRunRequestObject) (gen.CancelRunResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if err := h.Engine.CancelRun(ctx, info.ID, req.Id); err != nil {
		return nil, err
	}
	recordJobAudit(ctx, h.Engine, "run_canceled", "pipeline_run", req.Id)
	return gen.CancelRun204Response{}, nil
}

// toStepListDTO converts a page of steps into the wire list DTO, adding a
// nextCursor when the page was full (matching the cursor convention used
// by every other list endpoint in this codebase, e.g. auditapi.ListAudit).
func toStepListDTO(steps []dbgen.PipelineStep, limit int32) gen.PipelineStepList {
	items := make([]gen.PipelineStep, 0, len(steps))
	for _, s := range steps {
		items = append(items, toStepDTO(s))
	}
	list := gen.PipelineStepList{Items: items}
	if int32(len(steps)) == limit && len(steps) > 0 {
		next := httpx.EncodeCursor(idconv.FromPg(steps[len(steps)-1].ID))
		list.NextCursor = &next
	}
	return list
}
