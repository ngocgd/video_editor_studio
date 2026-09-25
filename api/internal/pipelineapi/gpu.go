package pipelineapi

import (
	"context"
	"strings"

	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/workerstatus"
	"loomtale/api/internal/tenant"
)

// workerStatusReader is the subset of *workerstatus.Store this package
// depends on, narrowed to an interface so GetGpuStatus is testable
// against a fake without a database.
type workerStatusReader interface {
	Get(ctx context.Context) (workerstatus.Status, error)
}

var _ workerStatusReader = (*workerstatus.Store)(nil)

// splitProviderRef parses a "backend:model" ref as stored in
// worker_status.resident_ref (see pipeline.ModelRef's own
// Backend+":"+Model convention, e.g. gpu_executor.go's preferResident).
func splitProviderRef(ref string) (backend, model string, ok bool) {
	backend, model, found := strings.Cut(ref, ":")
	return backend, model, found
}

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
	} else if h.WorkerStatus != nil {
		// This process (cmd/api) has no network path to gpu_net; the
		// worker publishes its own status instead (see
		// api/internal/providers/workerstatus and cmd/worker's
		// heartbeat), and this is the only source of live VRAM/resident
		// data available here.
		applyWorkerStatus(ctx, &status, h.WorkerStatus)
	}

	return gen.GetGpuStatus200JSONResponse(status), nil
}

func applyWorkerStatus(ctx context.Context, status *gen.GpuStatus, store workerStatusReader) {
	ws, err := store.Get(ctx)
	if err != nil {
		return // no worker has ever reported; leave the fields unset
	}
	online := ws.Fresh
	status.WorkerOnline = &online
	if !ws.Fresh {
		return // a stale row's VRAM/resident data is not trustworthy
	}
	status.Vram = &gen.GpuVram{
		TotalMb: ws.GPU.TotalMB, FreeMb: ws.GPU.FreeMB, BudgetMb: ws.GPU.BudgetMB,
		RenderReserveMb: ws.GPU.RenderReserveMB, MeasuredAt: ws.GPU.MeasuredAt,
	}
	if ws.ResidentRef != "" {
		if backend, model, ok := splitProviderRef(ws.ResidentRef); ok {
			status.Resident = &gen.GpuResident{Backend: backend, Model: model}
		}
	}
	backends := make([]gen.GpuBackendStatus, 0, len(ws.Providers))
	for name, info := range ws.Providers {
		backends = append(backends, gen.GpuBackendStatus{Name: name, Reachable: info.Available})
	}
	status.Backends = backends
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
