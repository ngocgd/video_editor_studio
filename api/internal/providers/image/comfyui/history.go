package comfyui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// HistoryEntry is one prompt's recorded outputs, keyed by node id.
type HistoryEntry struct {
	Outputs map[string]struct {
		Images []struct {
			Filename  string `json:"filename"`
			Subfolder string `json:"subfolder"`
			Type      string `json:"type"`
		} `json:"images"`
	} `json:"outputs"`
}

// History fetches /history/{promptID}. A prompt id that has not finished
// yet returns an empty map with no error (callers poll).
func (c *Client) History(ctx context.Context, promptID string) (map[string]HistoryEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/history/"+url.PathEscape(promptID), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("comfyui: history request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("comfyui: history unexpected status %d", resp.StatusCode)
	}
	var out map[string]HistoryEntry
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("comfyui: decode history: %w", err)
	}
	return out, nil
}

// ViewURL builds the /view URL for a history image reference.
func (c *Client) ViewURL(filename, subfolder, kind string) string {
	q := url.Values{"filename": {filename}, "subfolder": {subfolder}, "type": {kind}}
	return c.BaseURL + "/view?" + q.Encode()
}

// FetchOutput downloads one output image's bytes via /view.
func (c *Client) FetchOutput(ctx context.Context, filename, subfolder, kind string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ViewURL(filename, subfolder, kind), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("comfyui: view request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("comfyui: view unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
