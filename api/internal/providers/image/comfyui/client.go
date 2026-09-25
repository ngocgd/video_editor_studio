// Package comfyui implements the ImageGenerator interface (phase 4
// contract: HTTP /prompt, WS progress, /history, /view, /upload/image,
// /free, /system_stats) against a ComfyUI server. Workflows are loaded as
// server-side JSON templates (comfyui/workflows/*.json) with parameter
// overrides applied by node id/input key, so this package stays
// workflow-agnostic: it never hardcodes a specific model graph.
package comfyui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client talks to one ComfyUI server over HTTP/WS. BaseURL has no
// trailing slash (e.g. "http://comfyui:8188").
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{BaseURL: baseURL, HTTP: httpClient}
}

// Override sets workflow[nodeID].inputs[inputKey] = value before
// submission, the "declared parameter map" callers use to fill in a
// template's variable fields (prompt text, seed, dimensions, ...).
type Override struct {
	NodeID   string
	InputKey string
	Value    any
}

// ApplyOverrides returns a deep-enough copy of workflow with every
// Override applied, without mutating the caller's template (templates
// are loaded once and reused across many requests).
func ApplyOverrides(workflow map[string]any, overrides []Override) (map[string]any, error) {
	out := make(map[string]any, len(workflow))
	for k, v := range workflow {
		out[k] = v
	}
	for _, ov := range overrides {
		nodeRaw, ok := out[ov.NodeID]
		if !ok {
			return nil, fmt.Errorf("comfyui: override references unknown node %q", ov.NodeID)
		}
		node, ok := nodeRaw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("comfyui: node %q is not an object", ov.NodeID)
		}
		nodeCopy := make(map[string]any, len(node))
		for k, v := range node {
			nodeCopy[k] = v
		}
		inputsRaw, _ := nodeCopy["inputs"].(map[string]any)
		inputsCopy := make(map[string]any, len(inputsRaw)+1)
		for k, v := range inputsRaw {
			inputsCopy[k] = v
		}
		inputsCopy[ov.InputKey] = ov.Value
		nodeCopy["inputs"] = inputsCopy
		out[ov.NodeID] = nodeCopy
	}
	return out, nil
}

type submitRequest struct {
	Prompt   map[string]any `json:"prompt"`
	ClientID string         `json:"client_id"`
}

type submitResponse struct {
	PromptID string `json:"prompt_id"`
}

// Submit posts workflow to /prompt and returns the resulting prompt id.
func (c *Client) Submit(ctx context.Context, workflow map[string]any, clientID string) (string, error) {
	payload, err := json.Marshal(submitRequest{Prompt: workflow, ClientID: clientID})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/prompt", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("comfyui: submit failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("comfyui: submit unexpected status %d: %s", resp.StatusCode, b)
	}
	var out submitResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("comfyui: decode submit response: %w", err)
	}
	return out.PromptID, nil
}
