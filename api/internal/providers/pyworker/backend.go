package pyworker

import (
	"context"

	"loomtale/api/internal/providers/residency"
)

// Backend adapts Client to residency.Backend. The worker registers one
// generic "pyworker" backend (Engine empty): the Python worker holds one
// engine at a time, and the model named in a ModelRef is the engine to
// load (manifest entry names and Python engine names are the same, e.g.
// "chatterbox", "whisper-align"). A non-empty Engine pins the backend to
// that one engine instead.
type Backend struct {
	Client *Client
	Engine string
	// Gate, if set, runs before every load with the model name: the
	// worker wires the licence and verified-files gate here, so the Python
	// worker is never asked to load files the app has not verified.
	Gate func(ctx context.Context, model string) error
}

var _ residency.Backend = (*Backend)(nil)

func (b *Backend) Name() string {
	if b.Engine == "" {
		return "pyworker"
	}
	return "pyworker:" + b.Engine
}

func (b *Backend) engineFor(model string) string {
	if b.Engine != "" {
		return b.Engine
	}
	return model
}

// Unload releases the pinned engine, or whatever engine is resident for
// the generic backend (an empty engine name unloads unconditionally).
func (b *Backend) Unload(ctx context.Context) error {
	return b.Client.UnloadModel(ctx, b.Engine)
}

func (b *Backend) Load(ctx context.Context, model string) (int64, error) {
	if b.Gate != nil {
		if err := b.Gate(ctx, model); err != nil {
			return 0, err
		}
	}
	return b.Client.LoadModel(ctx, b.engineFor(model), model)
}

// Probe implements residency.Prober via ListEngines.
func (b *Backend) Probe(ctx context.Context) (bool, []string) {
	engines, err := b.Client.ListEngines(ctx)
	if err != nil {
		return false, nil
	}
	loaded := []string{}
	for _, e := range engines {
		if e.Loaded {
			loaded = append(loaded, e.Name)
		}
	}
	return true, loaded
}

// Resident proves app-side, through ListEngines, that the engine for
// model is the one the Python worker holds.
func (b *Backend) Resident(ctx context.Context, model string) (bool, error) {
	engines, err := b.Client.ListEngines(ctx)
	if err != nil {
		return false, err
	}
	want := b.engineFor(model)
	for _, e := range engines {
		if e.Name == want {
			return e.Loaded, nil
		}
	}
	return false, nil
}
