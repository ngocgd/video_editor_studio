package comfyui

import (
	"context"
	"fmt"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/residency"
)

// ErrEngineNotInstalled wraps the phase 3 pipeline.ErrEngineNotInstalled
// sentinel so a StepHandler.Run that surfaces this error classifies
// correctly via pipeline.Classify. Returned by Load until phase 9a ships
// real workflow templates and their weights exist on disk: honesty over
// faking a load, per the phase 4 contract ("before phase 9 every GPU
// engine returns an honest engine_not_installed unless its weights
// exist").
var ErrEngineNotInstalled = fmt.Errorf("comfyui: %w", pipeline.ErrEngineNotInstalled)

// Backend adapts Client to residency.Backend. Unload is fully real
// (POST /free works against any ComfyUI server today); Load/Resident are
// honest stand-ins until phase 9a wires real workflow-based loading and
// a real "what's resident" check (ComfyUI's own /system_stats does not
// generically expose which checkpoint is loaded across every workflow
// shape).
type Backend struct {
	Client *Client
}

var _ residency.Backend = (*Backend)(nil)

func (b *Backend) Name() string { return "comfyui" }

func (b *Backend) Unload(ctx context.Context) error {
	return b.Client.Free(ctx)
}

func (b *Backend) Load(context.Context, string) (int64, error) {
	return 0, ErrEngineNotInstalled
}

func (b *Backend) Resident(context.Context, string) (bool, error) {
	return false, nil
}
