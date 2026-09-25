package pipelineapi

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"loomtale/api/internal/pipeline"
)

// RunBelongsToTenant implements sse.RunAuthorizer, letting the SSE
// handler reject an /events?topics=<runId> request for a run the caller
// does not own before ever subscribing it.
func (h *PipelineAPI) RunBelongsToTenant(ctx context.Context, tenantID, runID uuid.UUID) (bool, error) {
	_, err := h.Engine.GetRun(ctx, tenantID, runID)
	if errors.Is(err, pipeline.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
