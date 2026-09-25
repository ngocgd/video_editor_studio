package pyworker

import (
	"context"
	"time"

	"loomtale/api/internal/pipeline"
)

// Probe implements pipeline.GpuProbe via the Python worker's GpuStatus
// RPC (pynvml-backed) rather than shelling out to nvidia-smi directly:
// the Go worker's distroless runtime image has no /usr/bin for
// nvidia-container-toolkit to inject nvidia-smi into (verified: pyworker's
// Debian-based image gets it at /usr/bin/nvidia-smi, the Go worker's
// distroless image does not get it at all), so the Python side — which
// already needs pynvml for its own ListEngines/GpuStatus reporting — is
// the only reliable place to read aggregate VRAM from in this stack.
type Probe struct {
	Client          *Client
	RenderReserveMB int64
}

// Snapshot implements pipeline.GpuProbe. A pyworker RPC failure (e.g. not
// reachable yet during startup) is reported as GpuSnapshot{} rather than
// an error, matching the "no_gpu handled" contract: callers must never
// crash on a transient probe failure.
func (p *Probe) Snapshot(ctx context.Context) (pipeline.GpuSnapshot, error) {
	status, err := p.Client.GpuStatus(ctx)
	now := time.Now().UTC()
	if err != nil || !status.GPUPresent {
		return pipeline.GpuSnapshot{MeasuredAt: now, RenderReserveMB: p.RenderReserveMB}, nil
	}
	return pipeline.GpuSnapshot{
		TotalMB:         status.TotalMB,
		FreeMB:          status.FreeMB,
		RenderReserveMB: p.RenderReserveMB,
		MeasuredAt:      now,
	}, nil
}
