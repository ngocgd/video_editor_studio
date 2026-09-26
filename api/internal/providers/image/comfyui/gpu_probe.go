package comfyui

import (
	"context"
	"time"

	"loomtale/api/internal/pipeline"
)

// GpuProbe implements pipeline.GpuProbe from ComfyUI's /system_stats.
// The benchmark CLI uses it: it runs next to ComfyUI on the GPU network
// but without the Python worker the Go worker normally probes through.
type GpuProbe struct {
	Client          *Client
	RenderReserveMB int64
}

var _ pipeline.GpuProbe = (*GpuProbe)(nil)

// Snapshot reports card-wide total and free VRAM.
func (p *GpuProbe) Snapshot(ctx context.Context) (pipeline.GpuSnapshot, error) {
	stats, err := p.Client.SystemStats(ctx)
	if err != nil {
		return pipeline.GpuSnapshot{}, err
	}
	snap := pipeline.GpuSnapshot{RenderReserveMB: p.RenderReserveMB, MeasuredAt: time.Now().UTC()}
	if gpu, ok := stats.GPU(); ok {
		snap.TotalMB = gpu.VRAMTotal >> 20
		snap.FreeMB = gpu.VRAMFree >> 20
	}
	return snap, nil
}
