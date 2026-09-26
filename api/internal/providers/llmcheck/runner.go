package llmcheck

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// cancelTimeout bounds canceling a probe that timed out.
const cancelTimeout = 5 * time.Second

// Runner enqueues a probe step from the api process and waits for the
// worker to finish it.
type Runner struct {
	Engine  *pipeline.Engine
	Queries *dbgen.Queries
}

// Check probes provider for tenantID in the worker. It returns ok=false
// with a human-readable detail for a failed or timed-out probe, and an
// error only when the probe could not be enqueued or read at all. A
// probe that times out is canceled so it never runs late.
func (r *Runner) Check(ctx context.Context, tenantID uuid.UUID, createdBy *uuid.UUID, provider string) (ok bool, detail string, err error) {
	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := r.Engine.Enqueue(ctx, tenantID, pipeline.RunSpec{
		ID:        runID,
		ScopeKind: ScopeKind,
		ScopeID:   tenantID,
		Kind:      RunKind,
		CreatedBy: createdBy,
		Steps: []pipeline.StepSpec{{
			ID:        stepID,
			Kind:      Kind(provider),
			ScopeKind: ScopeKind,
			ScopeID:   tenantID,
			Priority:  pipeline.PriorityInteractive,
		}},
	}); err != nil {
		return false, "", fmt.Errorf("llmcheck: enqueue probe: %w", err)
	}

	status, errorMsg, err := Await(ctx, func(ctx context.Context) (string, string, error) {
		step, err := r.Queries.GetStepByID(ctx, dbgen.GetStepByIDParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(stepID)})
		if err != nil {
			return "", "", err
		}
		return step.Status, step.ErrorMsg.String, nil
	})
	if errors.Is(err, ErrTimeout) {
		// ctx is already done here; cancel on a fresh, short context.
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelTimeout)
		defer cancel()
		_ = r.Engine.CancelRun(cancelCtx, tenantID, runID)
		return false, "the worker did not answer in time (it may be offline or busy with other LLM steps)", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("llmcheck: read probe step: %w", err)
	}
	switch status {
	case "done":
		return true, "", nil
	case "canceled":
		return false, "the probe was canceled before it ran", nil
	default:
		return false, FailureDetail(errorMsg), nil
	}
}
