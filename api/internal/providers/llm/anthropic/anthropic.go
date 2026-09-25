// Package anthropic implements llm.Provider against the Anthropic Messages
// API directly over HTTP (rather than pulling in the full SDK surface):
// the request/response shape used here is the stable public REST
// contract, kept intentionally small since no tool use, vision or batch
// features are needed.
package anthropic

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
	"loomtale/api/internal/secretstr"

	"loomtale/api/internal/providers/llm"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	anthropicVersion = "2023-06-01"
)

// ErrStreamIncomplete is returned when the SSE stream ends (EOF or a
// mid-stream "error" event, e.g. overloaded_error) without a
// message_stop, so a truncated/partial response is never mistaken for a
// finished one. Left unwrapped: pipeline.Classify's default (transient)
// is the right policy for a dropped connection or an overload.
var ErrStreamIncomplete = errors.New("anthropic: stream ended without a terminal event")

// ErrRefused wraps pipeline.ErrValidation (permanent, no retry): the
// model declined to answer (stop_reason "refusal"), and retrying the
// identical prompt would refuse again.
var ErrRefused = fmt.Errorf("%w: anthropic: request refused", pipeline.ErrValidation)

// ErrMaxTokensTruncated is returned when the model stopped because it
// hit MaxTokens: transient by default classification (a caller may
// choose to retry with a larger budget), never stored as if it were a
// complete response.
var ErrMaxTokensTruncated = errors.New("anthropic: response truncated at max_tokens")

// Pricing is per-million-token USD cost, used only for CostUSD reporting;
// callers may override via WithPricing for a specific model.
type Pricing struct {
	InPerMTok  float64
	OutPerMTok float64
}

// Provider talks to the Anthropic Messages API.
type Provider struct {
	APIKey  secretstr.String
	Model   string
	BaseURL string
	Client  *http.Client
	Pricing Pricing
}

// New builds an Anthropic provider. client should have sane timeouts; no
// netguard is applied since this is a fixed, non-user-configurable host.
func New(apiKey secretstr.String, model string, client *http.Client) *Provider {
	return &Provider{APIKey: apiKey, Model: model, BaseURL: defaultBaseURL, Client: client}
}

func (p *Provider) Name() string { return "anthropic-api" }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesRequest struct {
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens"`
	System      string    `json:"system,omitempty"`
	Messages    []message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream"`
}

type contentBlockDeltaEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

type messageDeltaEvent struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// errorEvent is Anthropic's mid-stream SSE "error" event (e.g. type
// overloaded_error), distinct from an HTTP-level failure since it can
// arrive after a 200 response has already started streaming.
type errorEvent struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type messageStartEvent struct {
	Type    string `json:"type"`
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func (p *Provider) buildRequest(req llm.Request, stream bool) (messagesRequest, error) {
	nonce, err := llm.NewNonce()
	if err != nil {
		return messagesRequest{}, err
	}
	messages := make([]message, 0, len(req.Messages)+1)
	if data := llm.RenderDataBlocks(nonce, req.Data); data != "" {
		messages = append(messages, message{Role: "user", Content: data})
	}
	for _, m := range req.Messages {
		messages = append(messages, message{Role: m.Role, Content: m.Text})
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	return messagesRequest{
		Model:       p.Model,
		MaxTokens:   maxTokens,
		System:      req.System,
		Messages:    messages,
		Temperature: req.Temperature,
		Stream:      stream,
	}, nil
}

func (p *Provider) Generate(ctx context.Context, req llm.Request) (llm.Response, error) {
	return p.Stream(ctx, req, nil)
}

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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return llm.Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.APIKey.Reveal())
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return llm.Response{}, llm.ClassifyHTTP(resp.StatusCode, "anthropic", string(b))
	}

	var text bytes.Buffer
	var usage llm.Usage
	var stopReason string
	var sawMessageStop bool
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &probe); err != nil {
			continue
		}
		switch probe.Type {
		case "content_block_delta":
			var ev contentBlockDeltaEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil && ev.Delta.Text != "" {
				text.WriteString(ev.Delta.Text)
				if onDelta != nil {
					onDelta(llm.Delta{Text: ev.Delta.Text})
				}
			}
		case "message_start":
			var ev messageStartEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				usage.In = ev.Message.Usage.InputTokens
			}
		case "message_delta":
			var ev messageDeltaEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				usage.Out = ev.Usage.OutputTokens
				if ev.Delta.StopReason != "" {
					stopReason = ev.Delta.StopReason
				}
			}
		case "message_stop":
			sawMessageStop = true
		case "error":
			var ev errorEvent
			_ = json.Unmarshal([]byte(data), &ev)
			return llm.Response{}, fmt.Errorf("anthropic: mid-stream error (%s): %s", ev.Error.Type, ev.Error.Message)
		}
	}
	if err := scanner.Err(); err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: stream read: %w", err)
	}
	if !sawMessageStop {
		return llm.Response{}, ErrStreamIncomplete
	}
	if stopReason == "refusal" {
		return llm.Response{}, ErrRefused
	}
	if stopReason == "max_tokens" {
		return llm.Response{}, fmt.Errorf("%w: stopped at max_tokens=%d", ErrMaxTokensTruncated, req.MaxTokens)
	}

	cost := float64(usage.In)/1_000_000*p.Pricing.InPerMTok + float64(usage.Out)/1_000_000*p.Pricing.OutPerMTok
	return llm.Response{
		Text:     text.String(),
		Usage:    usage,
		CostUSD:  cost,
		Model:    p.Model,
		Provider: p.Name(),
		Duration: time.Since(start),
	}, nil
}
