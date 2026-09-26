// Package bench is the re-runnable model benchmark harness behind
// `loomtale bench --suite <name>`. It drives the real engines (the image
// suites here; later phases add TTS, align and LoRA suites), measures
// seconds per image, VRAM peak, container RSS peak and residency switch
// time, and writes one model_benchmarks row per case.
package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/image/comfyui"
)

// Gate limits for the full image stack (go/no-go): ComfyUI's container
// RSS must stay within its mem_limit and the VM must not lean on swap.
const (
	GateRSSLimitMB  = 10 * 1024
	GateSwapLimitMB = 2 * 1024
)

// Harness runs suites against one ComfyUI engine through the residency
// manager, so every model change is a real, timed residency switch.
type Harness struct {
	Engine    *comfyui.Engine
	Residency pipeline.ModelResidency
	Queries   dbgen.Querier
	// RSS reports the ComfyUI container's peak RSS between two times; nil
	// leaves rss_peak_mb empty (the host-side sampler is not running).
	RSS func(from, to time.Time) (int64, bool)
	// SwapUsedMB reads the VM's current swap use; nil skips swap.
	SwapUsedMB func() (int64, error)
	// OutDir, if set, receives every output image for inspection.
	OutDir string
	Log    func(format string, args ...any)
}

// CaseResult is one measured case.
type CaseResult struct {
	Case          Case
	OK            bool
	Err           error
	Seconds       float64
	VRAMPeakMB    int64
	TorchPeakMB   int64
	RSSPeakMB     int64
	HasRSS        bool
	SwitchSeconds float64
	Switched      bool
	Images        int
}

// Report is a whole suite run.
type Report struct {
	RunID      uuid.UUID
	Suite      Suite
	Results    []CaseResult
	SwapPeakMB int64
	HasSwap    bool
	// GateOK is set for gate suites: every case succeeded with no OOM,
	// RSS stayed within GateRSSLimitMB and swap within GateSwapLimitMB.
	GateOK      bool
	GateReasons []string
}

// Run executes every case of suite in order and records the results.
func (h *Harness) Run(ctx context.Context, suite Suite) (Report, error) {
	report := Report{RunID: idconv.NewV7(), Suite: suite}
	outputs := map[string][]byte{}

	swapPeak, stopSwap := h.sampleSwap()
	defer stopSwap()

	for _, c := range suite.Cases {
		res := h.runCase(ctx, c, outputs)
		report.Results = append(report.Results, res)
		if err := h.record(ctx, report.RunID, suite.Name, res); err != nil {
			return report, err
		}
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
	}
	stopSwap()
	report.SwapPeakMB, report.HasSwap = swapPeak()
	if suite.Gate {
		report.GateOK, report.GateReasons = evaluateGate(report)
	}
	return report, nil
}

func (h *Harness) runCase(ctx context.Context, c Case, outputs map[string][]byte) CaseResult {
	res := CaseResult{Case: c}

	var images map[string][]byte
	if c.RefFrom != "" {
		ref, ok := outputs[c.RefFrom]
		if !ok {
			res.Err = fmt.Errorf("bench: case %s needs the output of %s, which failed", c.Name, c.RefFrom)
			return res
		}
		images = map[string][]byte{"ref_image": ref}
	}

	target := pipeline.ModelRef{Backend: "comfyui", Model: c.Model}
	if current := h.Residency.Current(); current == nil || *current != target {
		start := time.Now()
		if err := h.Residency.Ensure(ctx, target); err != nil {
			res.Err = fmt.Errorf("bench: load %s: %w", c.Model, err)
			return res
		}
		res.SwitchSeconds, res.Switched = time.Since(start).Seconds(), true
	}

	params := map[string]any{"filename_prefix": "bench-" + c.Name}
	for k, v := range c.Params {
		params[k] = v
	}
	from := time.Now()
	out, err := h.Engine.Run(ctx, c.Workflow, params, images)
	to := time.Now()
	res.Seconds, res.VRAMPeakMB, res.TorchPeakMB = out.Seconds, out.VRAMPeakMB, out.TorchPeakMB
	if h.RSS != nil {
		res.RSSPeakMB, res.HasRSS = h.RSS(from, to)
	}
	if err != nil {
		res.Err = err
		return res
	}
	res.OK, res.Images = true, len(out.Images)
	outputs[c.Name] = out.Images[0]
	if h.OutDir != "" {
		name := filepath.Join(h.OutDir, c.Name+".png")
		if werr := os.WriteFile(name, out.Images[0], 0o644); werr != nil {
			h.logf("could not save %s: %v", name, werr)
		}
	}
	h.logf("%-20s %-22s %7.1fs  vram %6d MB  torch %6d MB  switch %6.1fs", c.Name, c.Model, res.Seconds, res.VRAMPeakMB, res.TorchPeakMB, res.SwitchSeconds)
	return res
}

func (h *Harness) record(ctx context.Context, runID uuid.UUID, suite string, res CaseResult) error {
	meta, _ := json.Marshal(map[string]any{
		"workflow": res.Case.Workflow, "torch_peak_mb": res.TorchPeakMB, "images": res.Images, "switched": res.Switched,
	})
	params := dbgen.InsertModelBenchmarkParams{
		ID: idconv.ToPg(idconv.NewV7()), RunID: idconv.ToPg(runID), Suite: suite,
		CaseName: res.Case.Name, Model: res.Case.Model, Ok: res.OK, Meta: meta,
	}
	if res.OK {
		params.Seconds = float8(res.Seconds)
		params.VramPeakMb = idconv.ToPgInt8(res.VRAMPeakMB)
	}
	if res.HasRSS {
		params.RssPeakMb = idconv.ToPgInt8(res.RSSPeakMB)
	}
	if res.Switched {
		params.SwitchSeconds = float8(res.SwitchSeconds)
	}
	if res.Err != nil {
		params.Error = idconv.ToPgText(res.Err.Error())
		h.logf("%-20s %-22s FAILED: %v", res.Case.Name, res.Case.Model, res.Err)
	}
	return h.Queries.InsertModelBenchmark(ctx, params)
}

// sampleSwap polls SwapUsedMB every second until stopped and returns a
// getter for the peak.
func (h *Harness) sampleSwap() (func() (int64, bool), func()) {
	if h.SwapUsedMB == nil {
		return func() (int64, bool) { return 0, false }, func() {}
	}
	var mu sync.Mutex
	var peak int64
	seen := false
	sample := func() {
		if v, err := h.SwapUsedMB(); err == nil {
			mu.Lock()
			peak, seen = max(peak, v), true
			mu.Unlock()
		}
	}
	sample()
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				sample()
			}
		}
	}()
	get := func() (int64, bool) {
		mu.Lock()
		defer mu.Unlock()
		return peak, seen
	}
	return get, func() { once.Do(func() { close(stop) }) }
}

func evaluateGate(r Report) (bool, []string) {
	var reasons []string
	for _, res := range r.Results {
		switch {
		case res.Err != nil && errors.Is(res.Err, pipeline.ErrGPUOOM):
			reasons = append(reasons, fmt.Sprintf("%s: GPU out of memory", res.Case.Name))
		case res.Err != nil:
			reasons = append(reasons, fmt.Sprintf("%s: %v", res.Case.Name, res.Err))
		}
		if res.HasRSS && res.RSSPeakMB > GateRSSLimitMB {
			reasons = append(reasons, fmt.Sprintf("%s: ComfyUI RSS %d MB over %d MB", res.Case.Name, res.RSSPeakMB, GateRSSLimitMB))
		}
		if !res.HasRSS {
			reasons = append(reasons, fmt.Sprintf("%s: no RSS sample (run the host-side sampler)", res.Case.Name))
		}
	}
	if !r.HasSwap {
		reasons = append(reasons, "no swap sample")
	} else if r.SwapPeakMB > GateSwapLimitMB {
		reasons = append(reasons, fmt.Sprintf("VM swap use %d MB over %d MB", r.SwapPeakMB, GateSwapLimitMB))
	}
	return len(reasons) == 0, reasons
}

func (h *Harness) logf(format string, args ...any) {
	if h.Log != nil {
		h.Log(format, args...)
	}
}
