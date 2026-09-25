package pipelineapi

import (
	"context"

	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// gpuQueuePageLimit bounds how many queued gpu steps /gpu ever returns;
// the queue view is a live operational snapshot, not a paginated list.
const gpuQueuePageLimit = 100

// GetGpuStatus implements gen.StrictServerInterface. Running/queue are
// scoped to the caller's own tenant (there is only one physical GPU, but
// a tenant should only see its own work in flight); vram/backends/
// encoder/capabilities come from GpuProbe, nil until phase 4 wires a real
// one, in which case they are simply omitted rather than faked.
func (h *PipelineAPI) GetGpuStatus(ctx context.Context, _ gen.GetGpuStatusRequestObject) (gen.GetGpuStatusResponseObject, error) {
	info := tenant.MustFromCtx(ctx)

	running, err := h.Engine.RunningGpuStep(ctx, info.ID)
	if err != nil {
		return nil, err
	}
	queued, err := h.Engine.GpuQueue(ctx, info.ID, gpuQueuePageLimit)
	if err != nil {
		return nil, err
	}

	status := gen.GpuStatus{
		Queue:        make([]gen.StepSummary, 0, len(queued)),
		Backends:     []gen.GpuBackendStatus{},
		Capabilities: []string{},
	}
	if running != nil {
		s := toStepSummary(*running)
		status.Running = &s
	}
	for _, s := range queued {
		status.Queue = append(status.Queue, toStepSummary(s))
	}

	if h.Residency != nil {
		if current := h.Residency.Current(); current != nil {
			status.Resident = &gen.GpuResident{Backend: current.Backend, Model: current.Model}
		}
	}

	if h.Probe != nil {
		snap, err := h.Probe.Snapshot(ctx)
		if err == nil {
			applySnapshot(&status, snap)
		}
		// A probe error is not fatal to the endpoint: the queue view above
		// is still accurate and useful without live VRAM/backend data.
	}

	return gen.GetGpuStatus200JSONResponse(status), nil
}

func applySnapshot(status *gen.GpuStatus, snap pipeline.GpuSnapshot) {
	status.Vram = &gen.GpuVram{
		TotalMb:         snap.TotalMB,
		FreeMb:          snap.FreeMB,
		BudgetMb:        snap.BudgetMB,
		RenderReserveMb: snap.RenderReserveMB,
		MeasuredAt:      snap.MeasuredAt,
	}
	backends := make([]gen.GpuBackendStatus, 0, len(snap.Backends))
	for _, b := range snap.Backends {
		backends = append(backends, gen.GpuBackendStatus{Name: b.Name, Reachable: b.Reachable, Loaded: b.Loaded})
	}
	status.Backends = backends
	if snap.Encoder != nil {
		status.Encoder = &gen.GpuEncoder{Name: snap.Encoder.Name, Hw: snap.Encoder.HW}
	}
	status.Capabilities = snap.Capabilities
}
