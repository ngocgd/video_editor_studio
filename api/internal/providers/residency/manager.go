package residency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"loomtale/api/internal/pipeline"
)

// vramPollInterval and vramPollTimeout implement the contract's "poll
// GpuProbe every 500ms until free_mb >= m.vram_mb + 512 (timeout 60s ->
// transient error)".
const (
	vramPollInterval  = 500 * time.Millisecond
	vramPollTimeout   = 60 * time.Second
	vramHeadroomMB    = 512
	residencyPollWait = 100 * time.Millisecond
)

// ErrVRAMTimeout is a transient error (retryable by the pipeline's
// classifier) returned when free VRAM never reaches the required budget
// within vramPollTimeout.
var ErrVRAMTimeout = errors.New("residency: timed out waiting for free VRAM")

// Backend is one GPU-backed service the residency manager can load/unload
// a model on.
type Backend interface {
	// Name matches the ModelRef.Backend value routed to this backend
	// (e.g. "ollama", "comfyui", "pyworker:tts").
	Name() string
	// Unload releases whatever this backend currently has resident, if
	// anything. Called on every Ensure (even for an unrelated backend)
	// and by UnloadAll.
	Unload(ctx context.Context) error
	// Load makes model resident on this backend.
	Load(ctx context.Context, model string) (vramHeldMB int64, err error)
	// Resident proves app-side (not via nvidia-smi) that model is
	// currently loaded on this backend.
	Resident(ctx context.Context, model string) (bool, error)
}

// Manager implements pipeline.ModelResidency and pipeline.GpuProbe.
type Manager struct {
	Probe    pipeline.GpuProbe
	Backends map[string]Backend
	// Manifests maps "backend:model" to its estimated VRAM footprint in
	// MB. A missing entry (the normal case before phases 9a-9c ship real
	// manifests) skips the VRAM-wait poll: Ensure loads immediately and
	// relies on the pipeline's own gpu_oom classification if it does not
	// fit.
	Manifests map[string]int64
	// BudgetMB is set once at boot (see NewManagerWithBudget) from the
	// measured free VRAM minus RenderReserveMB.
	BudgetMB int64

	mu      sync.Mutex
	current *pipeline.ModelRef
}

// NewManagerWithBudget measures free VRAM once via probe and returns a
// Manager with BudgetMB = free - renderReserveMB, per the contract ("at
// boot with nothing loaded, the probe measures free VRAM ... and sets
// budget_mb = free - render_reserve_mb").
func NewManagerWithBudget(ctx context.Context, probe pipeline.GpuProbe, backends map[string]Backend, manifests map[string]int64, renderReserveMB int64) (*Manager, error) {
	snap, err := probe.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("residency: initial VRAM measurement: %w", err)
	}
	budget := snap.FreeMB - renderReserveMB
	if budget < 0 {
		budget = 0
	}
	return &Manager{Probe: probe, Backends: backends, Manifests: manifests, BudgetMB: budget}, nil
}

// Snapshot implements pipeline.GpuProbe by delegating to Probe and
// filling in BudgetMB/Backends from this Manager's own state.
func (m *Manager) Snapshot(ctx context.Context) (pipeline.GpuSnapshot, error) {
	snap, err := m.Probe.Snapshot(ctx)
	if err != nil {
		return pipeline.GpuSnapshot{}, err
	}
	snap.BudgetMB = m.BudgetMB

	backends := make([]pipeline.BackendStatus, 0, len(m.Backends))
	for name, b := range m.Backends {
		status := pipeline.BackendStatus{Name: name}
		current := m.Current()
		if current != nil && current.Backend == name {
			if loaded, err := b.Resident(ctx, current.Model); err == nil && loaded {
				status.Reachable = true
				status.Loaded = []string{current.Model}
			}
		}
		backends = append(backends, status)
	}
	snap.Backends = backends
	return snap, nil
}

// Current implements pipeline.ModelResidency.
func (m *Manager) Current() *pipeline.ModelRef {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return nil
	}
	ref := *m.current
	return &ref
}

// Ensure implements pipeline.ModelResidency: unload every backend, wait
// for the target model's VRAM budget to actually free up (polling the
// probe, never trusting an immediate unload call to mean the memory is
// already released), load the target, then prove residency app-side.
func (m *Manager) Ensure(ctx context.Context, target pipeline.ModelRef) error {
	if err := m.UnloadAll(ctx); err != nil {
		return err
	}

	if needed := m.Manifests[target.Backend+":"+target.Model]; needed > 0 {
		if err := m.waitForFreeVRAM(ctx, needed); err != nil {
			return err
		}
	}

	backend, ok := m.Backends[target.Backend]
	if !ok {
		return fmt.Errorf("residency: unknown backend %q", target.Backend)
	}
	if _, err := backend.Load(ctx, target.Model); err != nil {
		return fmt.Errorf("residency: load %s:%s: %w", target.Backend, target.Model, err)
	}

	if err := m.proveResident(ctx, backend, target.Model); err != nil {
		return err
	}

	m.mu.Lock()
	ref := target
	m.current = &ref
	m.mu.Unlock()
	return nil
}

// UnloadAll implements pipeline.ModelResidency, used both by Ensure
// before a switch and directly by the phase 3 gpu_oom retry policy.
func (m *Manager) UnloadAll(ctx context.Context) error {
	var firstErr error
	for _, b := range m.Backends {
		if err := b.Unload(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("residency: unload %s: %w", b.Name(), err)
		}
	}
	m.mu.Lock()
	m.current = nil
	m.mu.Unlock()
	return firstErr
}

// waitForFreeVRAM polls Probe every vramPollInterval until at least
// neededMB+vramHeadroomMB is free, or vramPollTimeout elapses.
func (m *Manager) waitForFreeVRAM(ctx context.Context, neededMB int64) error {
	deadline := time.Now().Add(vramPollTimeout)
	ticker := time.NewTicker(vramPollInterval)
	defer ticker.Stop()
	for {
		snap, err := m.Probe.Snapshot(ctx)
		if err == nil && snap.FreeMB >= neededMB+vramHeadroomMB {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrVRAMTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// proveResident polls backend.Resident briefly: Load returning
// successfully does not by itself prove the model is actually serving
// (per the contract, nvidia-smi per-process data is not trusted either),
// so this waits for the backend's own status endpoint to agree.
func (m *Manager) proveResident(ctx context.Context, backend Backend, model string) error {
	deadline := time.Now().Add(vramPollTimeout)
	for {
		loaded, err := backend.Resident(ctx, model)
		if err == nil && loaded {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("residency: %s never confirmed %s resident within %s", backend.Name(), model, vramPollTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(residencyPollWait):
		}
	}
}
