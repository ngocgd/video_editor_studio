package pipelineapi

import (
	"context"
	"errors"
	"net/http"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// CreateRun implements gen.StrictServerInterface. RBAC guarantees an
// editor+ role in a resolved tenant for this route. Every id (the run's
// and every step's) is generated here, never accepted from the client:
// see resolveStepGraph. Priority is derived from the client's declared
// priorityClass, never a raw client-supplied integer.
func (h *PipelineAPI) CreateRun(ctx context.Context, req gen.CreateRunRequestObject) (gen.CreateRunResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	body := req.Body

	priority, err := priorityForClass(body.PriorityClass)
	if err != nil {
		detail := err.Error()
		return gen.CreateRun400ApplicationProblemPlusJSONResponse{Title: "invalid run", Status: http.StatusBadRequest, Detail: &detail}, nil
	}
	steps, err := resolveStepGraph(body.Steps, priority)
	if err != nil {
		detail := err.Error()
		return gen.CreateRun400ApplicationProblemPlusJSONResponse{Title: "invalid run", Status: http.StatusBadRequest, Detail: &detail}, nil
	}

	runID := idconv.NewV7()
	spec := pipeline.RunSpec{
		ID:        runID,
		ScopeKind: body.ScopeKind,
		ScopeID:   body.ScopeId,
		Kind:      body.Kind,
		Steps:     steps,
	}

	if _, err := h.Engine.Enqueue(ctx, info.ID, spec); err != nil {
		switch {
		case errors.Is(err, pipeline.ErrQuotaExceeded):
			detail := err.Error()
			return gen.CreateRun429ApplicationProblemPlusJSONResponse{Title: "quota exceeded", Status: http.StatusTooManyRequests, Detail: &detail}, nil
		case errors.Is(err, pipeline.ErrAdmissionDenied):
			detail := err.Error()
			return gen.CreateRun507ApplicationProblemPlusJSONResponse{Title: "admission denied", Status: http.StatusInsufficientStorage, Detail: &detail}, nil
		case errors.Is(err, pipeline.ErrInvalidGraph), errors.Is(err, pipeline.ErrUnknownStepKind):
			// The only two Enqueue error classes that are ever the
			// caller's own mistake: everything else (a database error, a
			// deadlock, anything unexpected) falls through to the
			// generic 500 handler below rather than echoing a raw
			// internal error message — and since every id here is
			// server-generated, a duplicate-key violation can no longer
			// happen at all, so there is nothing else genuinely
			// input-shaped left to surface as a 400.
			detail := err.Error()
			return gen.CreateRun400ApplicationProblemPlusJSONResponse{Title: "invalid run", Status: http.StatusBadRequest, Detail: &detail}, nil
		default:
			return nil, err
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
		detail := "invalid cursor"
		return gen.ListRunSteps400ApplicationProblemPlusJSONResponse{Title: "invalid request", Status: http.StatusBadRequest, Detail: &detail}, nil
	}
	limit := httpx.PageLimit(req.Params.Limit)

	steps, err := h.Engine.ListRunSteps(ctx, info.ID, req.Id, cursor, limit)
	if err != nil {
		return nil, err
	}
	return gen.ListRunSteps200JSONResponse(toStepListDTO(steps, limit, isEditorOrAbove(ctx))), nil
}

// CancelRun implements gen.StrictServerInterface.
func (h *PipelineAPI) CancelRun(ctx context.Context, req gen.CancelRunRequestObject) (gen.CancelRunResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if err := h.Engine.CancelRun(ctx, info.ID, req.Id); err != nil {
		if errors.Is(err, pipeline.ErrNotFound) {
			detail := "run not found, or already finished"
			return gen.CancelRun404ApplicationProblemPlusJSONResponse{Title: "not found", Status: http.StatusNotFound, Detail: &detail}, nil
		}
		return nil, err
	}
	recordJobAudit(ctx, h.Engine, "run_canceled", "pipeline_run", req.Id)
	return gen.CancelRun204Response{}, nil
}

// toStepListDTO converts a page of steps into the wire list DTO, adding a
// nextCursor when the page was full (matching the cursor convention used
// by every other list endpoint in this codebase, e.g. auditapi.ListAudit).
func toStepListDTO(steps []dbgen.PipelineStep, limit int32, includeErrorMsg bool) gen.PipelineStepList {
	items := make([]gen.PipelineStep, 0, len(steps))
	for _, s := range steps {
		items = append(items, toStepDTO(s, includeErrorMsg))
	}
	list := gen.PipelineStepList{Items: items}
	if int32(len(steps)) == limit && len(steps) > 0 {
		next := httpx.EncodeCursor(idconv.FromPg(steps[len(steps)-1].ID))
		list.NextCursor = &next
	}
	return list
}
