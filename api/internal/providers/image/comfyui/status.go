package comfyui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Free calls POST /free {unload_models, free_memory}, used by the
// residency manager to release VRAM before loading a different backend.
func (c *Client) Free(ctx context.Context) error {
	payload, _ := json.Marshal(map[string]bool{"unload_models": true, "free_memory": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/free", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("comfyui: free request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("comfyui: free unexpected status %d", resp.StatusCode)
	}
	return nil
}

// SystemStats is the app-side residency proof source (never nvidia-smi
// per-process data): the loaded checkpoint/unet names ComfyUI itself
// reports.
type SystemStats struct {
	Devices []struct {
		Name      string `json:"name"`
		VRAMTotal int64  `json:"vram_total"`
		VRAMFree  int64  `json:"vram_free"`
	} `json:"devices"`
}

// SystemStats fetches GET /system_stats.
func (c *Client) SystemStats(ctx context.Context) (SystemStats, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/system_stats", nil)
	if err != nil {
		return SystemStats{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return SystemStats{}, fmt.Errorf("comfyui: system_stats request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return SystemStats{}, fmt.Errorf("comfyui: system_stats unexpected status %d", resp.StatusCode)
	}
	var out SystemStats
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SystemStats{}, fmt.Errorf("comfyui: decode system_stats: %w", err)
	}
	return out, nil
}
