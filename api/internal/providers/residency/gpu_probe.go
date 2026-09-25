// Package residency implements the phase 3 pact (pipeline.ModelResidency,
// pipeline.GpuProbe): keeping exactly one GPU-backed backend resident at
// a time, unloading everything before a switch, and only reporting a
// model loaded once every backend's own status endpoint agrees (never
// trusting nvidia-smi's per-process breakdown, which is unreliable under
// WDDM).
package residency

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"loomtale/api/internal/pipeline"
)

// NvidiaSMIProbe reads aggregate (not per-process) VRAM totals via
// `nvidia-smi --query-gpu=...`, which the phase 1b spike found reliable
// on this desktop's WDDM driver, unlike nvidia-smi's per-process table.
type NvidiaSMIProbe struct {
	// Binary defaults to "nvidia-smi" (resolved via PATH); overridable
	// for tests.
	Binary string
	// RenderReserveMB is subtracted from measured free VRAM to leave
	// headroom for the concurrent NVENC render path.
	RenderReserveMB int64
	// Run executes cmd and returns combined stdout; overridable for
	// tests so no real GPU or nvidia-smi binary is required.
	Run func(ctx context.Context, binary string, args ...string) ([]byte, error)
}

func defaultRun(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Snapshot implements pipeline.GpuProbe. A missing nvidia-smi binary or a
// non-zero exit (no GPU present, or driver not ready) is reported as
// GpuSnapshot{TotalMB:0, FreeMB:0} rather than an error, per the
// contract's "no_gpu handled" requirement: callers treat a zero snapshot
// as "no GPU available" rather than crashing the /gpu endpoint.
func (p *NvidiaSMIProbe) Snapshot(ctx context.Context) (pipeline.GpuSnapshot, error) {
	run := p.Run
	if run == nil {
		run = defaultRun
	}
	binary := p.Binary
	if binary == "" {
		binary = "nvidia-smi"
	}

	out, err := run(ctx, binary, "--query-gpu=memory.total,memory.free", "--format=csv,noheader,nounits")
	now := time.Now().UTC()
	if err != nil {
		return pipeline.GpuSnapshot{MeasuredAt: now, RenderReserveMB: p.RenderReserveMB}, nil
	}

	total, free, ok := parseMemoryLine(string(out))
	if !ok {
		return pipeline.GpuSnapshot{MeasuredAt: now, RenderReserveMB: p.RenderReserveMB}, nil
	}
	return pipeline.GpuSnapshot{
		TotalMB:         total,
		FreeMB:          free,
		RenderReserveMB: p.RenderReserveMB,
		MeasuredAt:      now,
	}, nil
}

// parseMemoryLine parses nvidia-smi's "total, free" CSV line (only the
// first GPU is used; this desktop has exactly one).
func parseMemoryLine(out string) (total, free int64, ok bool) {
	line := strings.TrimSpace(out)
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	parts := strings.Split(line, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	t, err1 := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	f, err2 := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return t, f, true
}
