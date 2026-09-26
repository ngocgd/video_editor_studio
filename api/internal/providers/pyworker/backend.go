package pyworker

import (
	"context"
	"errors"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/residency"
)

// Backend adapts Client to residency.Backend for a specific engine slot
// (residency's Backends map is keyed by ModelRef.Backend, e.g.
// "pyworker:tts" for the TTS engine currently resident on the worker).
type Backend struct {
	Client *Client
	Engine string
}

var _ residency.Backend = (*Backend)(nil)

func (b *Backend) Name() string { return "pyworker:" + b.Engine }

func (b *Backend) Unload(ctx context.Context) error {
	return b.Client.UnloadModel(ctx, b.Engine)
}

func (b *Backend) Load(ctx context.Context, model string) (int64, error) {
	held, err := b.Client.LoadModel(ctx, b.Engine, model)
	if errors.Is(err, pipeline.ErrEngineNotInstalled) {
		return 0, err
	}
	return held, err
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

func (b *Backend) Resident(ctx context.Context, model string) (bool, error) {
	engines, err := b.Client.ListEngines(ctx)
	if err != nil {
		return false, err
	}
	for _, e := range engines {
		if e.Name == b.Engine {
			return e.Loaded, nil
		}
	}
	_ = model // the worker.proto contract reports one resident engine at a time, not per-model
	return false, nil
}
