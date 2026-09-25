package residency

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"loomtale/api/internal/pipeline"
)

// fakeProbe reports a fixed free VRAM until Advance is called, so tests
// can simulate VRAM only becoming available after N polls.
type fakeProbe struct {
	freeMB    atomic.Int64
	pollCount atomic.Int64
}

func newFakeProbe(freeMB int64) *fakeProbe {
	p := &fakeProbe{}
	p.freeMB.Store(freeMB)
	return p
}

func (p *fakeProbe) Snapshot(context.Context) (pipeline.GpuSnapshot, error) {
	p.pollCount.Add(1)
	return pipeline.GpuSnapshot{TotalMB: 16384, FreeMB: p.freeMB.Load()}, nil
}

type fakeBackend struct {
	name         string
	loadedModel  atomic.Value // string
	unloadCalled atomic.Bool
	loadCalled   atomic.Bool
	loadErr      error
}

func (b *fakeBackend) Name() string { return b.name }
func (b *fakeBackend) Unload(context.Context) error {
	b.unloadCalled.Store(true)
	b.loadedModel.Store("")
	return nil
}
func (b *fakeBackend) Load(_ context.Context, model string) (int64, error) {
	b.loadCalled.Store(true)
	if b.loadErr != nil {
		return 0, b.loadErr
	}
	b.loadedModel.Store(model)
	return 1024, nil
}
func (b *fakeBackend) Resident(_ context.Context, model string) (bool, error) {
	current, _ := b.loadedModel.Load().(string)
	return current == model, nil
}

func TestEnsureLoadsAndRecordsCurrent(t *testing.T) {
	probe := newFakeProbe(8000)
	backend := &fakeBackend{name: "ollama"}
	mgr := &Manager{Probe: probe, Backends: map[string]Backend{"ollama": backend}}

	target := pipeline.ModelRef{Backend: "ollama", Model: "llama3"}
	if err := mgr.Ensure(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if !backend.loadCalled.Load() {
		t.Fatal("expected Load to be called")
	}
	if got := mgr.Current(); got == nil || *got != target {
		t.Fatalf("Current() = %+v, want %+v", got, target)
	}
}

// TestEnsureNeverLoadsBeforeFreeVRAMConfirmed is the core residency
// safety property: with a manifest VRAM estimate set, Ensure must poll
// the probe until enough VRAM is actually free before calling Load, not
// just after issuing Unload.
func TestEnsureNeverLoadsBeforeFreeVRAMConfirmed(t *testing.T) {
	probe := newFakeProbe(100) // not enough for the 4000MB manifest below
	backend := &fakeBackend{name: "comfyui"}
	mgr := &Manager{
		Probe:     probe,
		Backends:  map[string]Backend{"comfyui": backend},
		Manifests: map[string]int64{"comfyui:qwen-image-edit": 4000},
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		if backend.loadCalled.Load() {
			return // too late to matter, but this goroutine only frees VRAM
		}
		probe.freeMB.Store(5000)
	}()

	target := pipeline.ModelRef{Backend: "comfyui", Model: "qwen-image-edit"}
	if err := mgr.Ensure(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if probe.pollCount.Load() < 2 {
		t.Fatalf("expected the probe to be polled more than once while waiting, got %d", probe.pollCount.Load())
	}
	if !backend.loadCalled.Load() {
		t.Fatal("expected Load to eventually be called once VRAM freed up")
	}
}

func TestEnsureTimesOutWhenVRAMNeverFrees(t *testing.T) {
	t.Parallel()
	probe := newFakeProbe(0)
	backend := &fakeBackend{name: "comfyui"}
	mgr := &Manager{
		Probe:     probe,
		Backends:  map[string]Backend{"comfyui": backend},
		Manifests: map[string]int64{"comfyui:huge-model": 999999},
	}
	// This test intentionally does not wait the full 60s timeout;
	// instead it verifies Load is never called while VRAM stays at 0
	// during a short observation window, then cancels via context.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := mgr.Ensure(ctx, pipeline.ModelRef{Backend: "comfyui", Model: "huge-model"})
	if err == nil {
		t.Fatal("expected an error when context is cancelled before VRAM frees up")
	}
	if backend.loadCalled.Load() {
		t.Fatal("Load must never be called before the VRAM budget is confirmed")
	}
}

func TestUnloadAllClearsCurrentAndCallsEveryBackend(t *testing.T) {
	probe := newFakeProbe(8000)
	ollama := &fakeBackend{name: "ollama"}
	ollama.loadedModel.Store("llama3")
	comfy := &fakeBackend{name: "comfyui"}
	mgr := &Manager{Probe: probe, Backends: map[string]Backend{"ollama": ollama, "comfyui": comfy}}
	mgr.current = &pipeline.ModelRef{Backend: "ollama", Model: "llama3"}

	if err := mgr.UnloadAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !ollama.unloadCalled.Load() || !comfy.unloadCalled.Load() {
		t.Fatal("expected Unload to be called on every backend")
	}
	if mgr.Current() != nil {
		t.Fatal("expected Current() to be nil after UnloadAll")
	}
}

func TestNewManagerWithBudgetSubtractsRenderReserve(t *testing.T) {
	probe := newFakeProbe(10900)
	mgr, err := NewManagerWithBudget(context.Background(), probe, nil, nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if mgr.BudgetMB != 10900-1024 {
		t.Fatalf("BudgetMB = %d, want %d", mgr.BudgetMB, 10900-1024)
	}
}
