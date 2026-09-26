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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
)

// ErrStreamIncomplete is returned when the NDJSON stream ends without a
// final done:true chunk, so a dropped connection is never mistaken for
// a completed response.
var ErrStreamIncomplete = errors.New("ollama: stream ended without a done:true chunk")

// classifyMidStreamError handles a {"error":"..."} chunk arriving after
// an already-200 response, where there is no HTTP status code to key
// off: llm.ClassifyOllamaError's OOM string match still applies, but a
// non-OOM message falls back to a plain (transient-by-default) error
// rather than a fabricated status code.
func classifyMidStreamError(message string) error {
	if err := llm.ClassifyOllamaError(0, message); errors.Is(err, pipeline.ErrGPUOOM) {
		return err
	}
	return fmt.Errorf("ollama: mid-stream error: %s", message)
}

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
	Model     string         `json:"model"`
	Messages  []chatMessage  `json:"messages"`
	Stream    bool           `json:"stream"`
	KeepAlive string         `json:"keep_alive,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}

type chatResponseChunk struct {
	Message    chatMessage `json:"message"`
	Done       bool        `json:"done"`
	EvalCount  int         `json:"eval_count"`
	PromptEval int         `json:"prompt_eval_count"`
	// Error is Ollama's own mid-stream error shape: a line like
	// {"error":"..."} instead of the usual {"message":...,"done":...}.
	Error string `json:"error"`
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
		return llm.Response{}, llm.ClassifyOllamaError(resp.StatusCode, string(b))
	}

	var text bytes.Buffer
	var final chatResponseChunk
	var sawDone bool
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
		if chunk.Error != "" {
			return llm.Response{}, classifyMidStreamError(chunk.Error)
		}
		if chunk.Message.Content != "" {
			text.WriteString(chunk.Message.Content)
			if onDelta != nil {
				onDelta(llm.Delta{Text: chunk.Message.Content})
			}
		}
		if chunk.Done {
			final = chunk
			sawDone = true
		}
	}
	if err := scanner.Err(); err != nil {
		return llm.Response{}, fmt.Errorf("ollama: stream read: %w", err)
	}
	if !sawDone {
		return llm.Response{}, ErrStreamIncomplete
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
	return p.unloadModel(ctx, p.Model)
}

func (p *Provider) unloadModel(ctx context.Context, model string) error {
	payload, err := json.Marshal(chatRequest{Model: model, Messages: nil, Stream: false, KeepAlive: "0"})
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
	running, err := p.Running(ctx)
	if err != nil {
		return false, err
	}
	for _, name := range running {
		if normalizeModelTag(name) == normalizeModelTag(p.Model) {
			return true, nil
		}
	}
	return false, nil
}

// Running lists every model /api/ps reports as resident.
func (p *Provider) Running(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: /api/ps request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: /api/ps unexpected status %d", resp.StatusCode)
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama: decode /api/ps: %w", err)
	}
	names := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// normalizeModelTag strips Ollama's implicit ":latest" tag so a
// configured model name without an explicit tag still matches what
// /api/ps reports (it always includes a tag, defaulting to "latest").
func normalizeModelTag(name string) string {
	return strings.TrimSuffix(name, ":latest")
}
