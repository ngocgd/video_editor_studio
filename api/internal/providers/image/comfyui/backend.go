package comfyui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/residency"
)

// ErrEngineNotInstalled wraps the pipeline.ErrEngineNotInstalled
// sentinel so a step that targets a model with no workflow, or whose
// files are not installed, fails permanently instead of retrying.
var ErrEngineNotInstalled = fmt.Errorf("comfyui: %w", pipeline.ErrEngineNotInstalled)

// defaultMinResidentMB is the least PyTorch memory ComfyUI must hold
// after a load for Resident to believe a model is actually in VRAM.
// Every image model here is several GB; an idle ComfyUI holds ~0.
const defaultMinResidentMB = 1024

// Backend adapts Engine to residency.Backend. ComfyUI has no "load
// model" call: models load when a workflow runs and stay cached until
// /free or memory pressure evicts them. Load therefore runs the model's
// warm-up (its first workflow at a tiny size and one step), and Resident
// requires both that this backend loaded the model last and that
// ComfyUI's own /system_stats shows its PyTorch allocator holding VRAM.
type Backend struct {
	Engine *Engine
	// Warmups maps a model name to the workflow that loads it.
	Warmups map[string]string
	// Gate, if set, runs before every load: the licence check and the
	// installed-files check (see models.LoadGate). A refused model never
	// reaches ComfyUI.
	Gate func(ctx context.Context, model string) error
	// MinResidentMB defaults to defaultMinResidentMB.
	MinResidentMB int64

	mu     sync.Mutex
	loaded string
}

var _ residency.Backend = (*Backend)(nil)

func (b *Backend) Name() string { return "comfyui" }

// Unload asks ComfyUI to drop every cached model and free its memory.
func (b *Backend) Unload(ctx context.Context) error {
	b.mu.Lock()
	b.loaded = ""
	b.mu.Unlock()
	return b.Engine.Client.Free(ctx)
}

// Load gates model, then runs its warm-up workflow so every file the
// model's workflows use is loaded by ComfyUI. It returns the PyTorch
// VRAM ComfyUI holds afterwards.
func (b *Backend) Load(ctx context.Context, model string) (int64, error) {
	workflow, ok := b.Warmups[model]
	if !ok {
		return 0, fmt.Errorf("%w: no ComfyUI workflow for model %q", ErrEngineNotInstalled, model)
	}
	if b.Gate != nil {
		if err := b.Gate(ctx, model); err != nil {
			return 0, err
		}
	}
	tpl, ok := b.Engine.Templates[workflow]
	if !ok {
		return 0, fmt.Errorf("%w: workflow %q is missing", ErrEngineNotInstalled, workflow)
	}

	params := map[string]any{}
	for k, v := range tpl.Map.Warmup {
		params[k] = v
	}
	if _, ok := tpl.Map.Params["prompt"]; ok {
		params["prompt"] = "warm-up"
	}
	images := map[string][]byte{}
	for _, name := range tpl.ImageParams() {
		images[name] = warmupImage()
	}
	res, err := b.Engine.Run(ctx, workflow, params, images)
	if err != nil {
		return 0, fmt.Errorf("comfyui: warm-up for %s: %w", model, err)
	}
	b.mu.Lock()
	b.loaded = model
	b.mu.Unlock()
	return res.TorchPeakMB, nil
}

// Resident implements residency.Backend (see the type comment).
func (b *Backend) Resident(ctx context.Context, model string) (bool, error) {
	b.mu.Lock()
	loaded := b.loaded
	b.mu.Unlock()
	if loaded != model || model == "" {
		return false, nil
	}
	held, err := b.torchMB(ctx)
	if err != nil {
		return false, err
	}
	return held >= b.minResidentMB(), nil
}

// Probe reports whether ComfyUI answers and which model it holds, for
// the worker's status heartbeat.
func (b *Backend) Probe(ctx context.Context) (bool, []string) {
	held, err := b.torchMB(ctx)
	if err != nil {
		return false, nil
	}
	b.mu.Lock()
	loaded := b.loaded
	b.mu.Unlock()
	if loaded == "" || held < b.minResidentMB() {
		return true, []string{}
	}
	return true, []string{loaded}
}

func (b *Backend) torchMB(ctx context.Context) (int64, error) {
	stats, err := b.Engine.Client.SystemStats(ctx)
	if err != nil {
		return 0, err
	}
	gpu, ok := stats.GPU()
	if !ok {
		return 0, nil
	}
	return gpu.TorchVRAMTotal >> 20, nil
}

func (b *Backend) minResidentMB() int64 {
	if b.MinResidentMB > 0 {
		return b.MinResidentMB
	}
	return defaultMinResidentMB
}

// warmupImage is a small solid-grey PNG used as the reference input of
// an image-edit warm-up: the content is irrelevant, only that the model
// runs once.
func warmupImage() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: 128, G: 128, B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
