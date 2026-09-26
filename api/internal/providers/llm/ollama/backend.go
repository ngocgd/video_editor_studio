package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/residency"
)

// Backend adapts Provider to residency.Backend for the residency
// manager. Residency is proven through /api/ps, never nvidia-smi.
type Backend struct {
	Provider *Provider
	// Prepare, if set, runs before every load: the worker wires it to the
	// licence and verified-files gate plus the offline import, so a model
	// that is not yet in Ollama's store is created from its pinned GGUF on
	// first use. Nil loads whatever Ollama already has.
	Prepare func(ctx context.Context, model string) error
}

var _ residency.Backend = (*Backend)(nil)

func (b *Backend) Name() string { return "ollama" }

// Unload releases every model /api/ps reports, not only the configured
// one: a benchmark or an earlier configuration may have loaded another.
func (b *Backend) Unload(ctx context.Context) error {
	running, err := b.Provider.Running(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range running {
		if err := b.Provider.unloadModel(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Load makes model resident without generating text, via /api/generate
// with an empty prompt (Ollama's documented "just load" pattern).
func (b *Backend) Load(ctx context.Context, model string) (int64, error) {
	if b.Prepare != nil {
		if err := b.Prepare(ctx, model); err != nil {
			return 0, err
		}
	}
	payload, err := json.Marshal(map[string]any{"model": model, "prompt": ""})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.Provider.BaseURL+"/api/generate", bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.Provider.Client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("ollama: load request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, llm.ClassifyOllamaError(resp.StatusCode, string(msg))
	}
	return 0, nil
}

// Probe implements residency.Prober via /api/ps.
func (b *Backend) Probe(ctx context.Context) (bool, []string) {
	running, err := b.Provider.Running(ctx)
	if err != nil {
		return false, nil
	}
	return true, running
}

// Resident reports whether /api/ps lists model.
func (b *Backend) Resident(ctx context.Context, model string) (bool, error) {
	running, err := b.Provider.Running(ctx)
	if err != nil {
		return false, err
	}
	for _, name := range running {
		if normalizeModelTag(name) == normalizeModelTag(model) {
			return true, nil
		}
	}
	return false, nil
}
