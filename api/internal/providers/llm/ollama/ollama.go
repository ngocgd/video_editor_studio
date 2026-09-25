// Package ollama implements llm.Provider against a local Ollama server's
// plain HTTP /api/chat NDJSON endpoint. Ollama is the required local
// default backend (compose.gpu.yml runs it with no profile); it has no
// model loaded until phase 9b.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"loomtale/api/internal/providers/llm"
)

// Provider talks to a single Ollama server over HTTP.
type Provider struct {
	BaseURL string
	Model   string
	Client  *http.Client
}

// New builds an Ollama provider. client must already be netguard-wrapped
// by the caller (see api/internal/netguard).
func New(baseURL, model string, client *http.Client) *Provider {
	return &Provider{BaseURL: baseURL, Model: model, Client: client}
}

func (p *Provider) Name() string { return "ollama" }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest mirrors Ollama's /api/chat body. KeepAlive is set to "0" by
// Unload so residency.Ensure can force this backend out of VRAM.
type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	KeepAlive string        `json:"keep_alive,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}

type chatResponseChunk struct {
	Message    chatMessage `json:"message"`
	Done       bool        `json:"done"`
	EvalCount  int         `json:"eval_count"`
	PromptEval int         `json:"prompt_eval_count"`
}

func (p *Provider) buildRequest(req llm.Request, stream bool) (chatRequest, error) {
	nonce, err := llm.NewNonce()
	if err != nil {
		return chatRequest{}, err
	}
	messages := make([]chatMessage, 0, len(req.Messages)+2)
	if req.System != "" {
		messages = append(messages, chatMessage{Role: "system", Content: req.System})
	}
	if data := llm.RenderDataBlocks(nonce, req.Data); data != "" {
		messages = append(messages, chatMessage{Role: "user", Content: data})
	}
	for _, m := range req.Messages {
		messages = append(messages, chatMessage{Role: m.Role, Content: m.Text})
	}
	opts := map[string]any{}
	if req.Temperature != 0 {
		opts["temperature"] = req.Temperature
	}
	if req.MaxTokens != 0 {
		opts["num_predict"] = req.MaxTokens
	}
	return chatRequest{Model: p.Model, Messages: messages, Stream: stream, Options: opts}, nil
}

// Generate implements llm.Provider without streaming.
func (p *Provider) Generate(ctx context.Context, req llm.Request) (llm.Response, error) {
	return p.Stream(ctx, req, nil)
}

// Stream implements llm.Provider. onDelta may be nil (used by Generate).
func (p *Provider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	start := time.Now()
	body, err := p.buildRequest(req, true)
	if err != nil {
		return llm.Response{}, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return llm.Response{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return llm.Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return llm.Response{}, fmt.Errorf("ollama: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return llm.Response{}, fmt.Errorf("ollama: unexpected status %d: %s", resp.StatusCode, b)
	}

	var text bytes.Buffer
	var final chatResponseChunk
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk chatResponseChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			return llm.Response{}, fmt.Errorf("ollama: decode chunk: %w", err)
		}
		if chunk.Message.Content != "" {
			text.WriteString(chunk.Message.Content)
			if onDelta != nil {
				onDelta(llm.Delta{Text: chunk.Message.Content})
			}
		}
		if chunk.Done {
			final = chunk
		}
	}
	if err := scanner.Err(); err != nil {
		return llm.Response{}, fmt.Errorf("ollama: stream read: %w", err)
	}

	return llm.Response{
		Text:     text.String(),
		Usage:    llm.Usage{In: final.PromptEval, Out: final.EvalCount},
		CostUSD:  0, // local inference has no per-token cost
		Model:    p.Model,
		Provider: p.Name(),
		Duration: time.Since(start),
	}, nil
}

// Unload sets keep_alive:0 so Ollama immediately releases this model from
// VRAM, used by the residency manager's Ensure before loading a different
// backend.
func (p *Provider) Unload(ctx context.Context) error {
	payload, err := json.Marshal(chatRequest{Model: p.Model, Messages: nil, Stream: false, KeepAlive: "0"})
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama: unload request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

// Loaded queries /api/ps and reports whether p.Model is currently
// resident, used by the residency manager's app-side proof step.
func (p *Provider) Loaded(ctx context.Context) (bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/api/ps", nil)
	if err != nil {
		return false, err
	}
	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return false, fmt.Errorf("ollama: /api/ps request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("ollama: /api/ps unexpected status %d", resp.StatusCode)
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("ollama: decode /api/ps: %w", err)
	}
	for _, m := range out.Models {
		if m.Name == p.Model {
			return true, nil
		}
	}
	return false, nil
}
