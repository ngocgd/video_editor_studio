package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"loomtale/api/internal/providers/residency"
)

// Backend adapts Provider to residency.Backend for the residency
// manager. Load and Resident/Unload are real against any Ollama server;
// no model exists to load until phase 9b seeds one, at which point Load
// simply targets that model.
type Backend struct {
	Provider *Provider
}

var _ residency.Backend = (*Backend)(nil)

func (b *Backend) Name() string { return "ollama" }

func (b *Backend) Unload(ctx context.Context) error {
	return b.Provider.Unload(ctx)
}

// Load pulls model into VRAM without generating text, via /api/generate
// with an empty prompt (Ollama's documented "just load" pattern).
func (b *Backend) Load(ctx context.Context, model string) (int64, error) {
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
		return 0, fmt.Errorf("ollama: load unexpected status %d", resp.StatusCode)
	}
	return 0, nil
}

// Probe implements residency.Prober via /api/ps.
func (b *Backend) Probe(ctx context.Context) (bool, []string) {
	loaded, err := b.Provider.Loaded(ctx)
	if err != nil {
		return false, nil
	}
	if loaded && b.Provider.Model != "" {
		return true, []string{b.Provider.Model}
	}
	return true, []string{}
}

func (b *Backend) Resident(ctx context.Context, model string) (bool, error) {
	loaded, err := b.Provider.Loaded(ctx)
	if err != nil || !loaded {
		return false, err
	}
	return normalizeModelTag(b.Provider.Model) == normalizeModelTag(model), nil
}
