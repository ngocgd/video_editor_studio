// Package gemini implements llm.Provider against the Gemini
// streamGenerateContent REST API directly over HTTP, for the same reason
// as the anthropic package: a small, stable REST surface instead of the
// full SDK.
package gemini

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

const defaultBaseURL = "https://generativelanguage.googleapis.com"

// ErrStreamIncomplete is returned when the stream ends without any chunk
// ever reporting a finishReason, so a dropped connection is never
// mistaken for a completed response.
var ErrStreamIncomplete = errors.New("gemini: stream ended without a finishReason")

// ErrMaxTokensTruncated mirrors the anthropic package's sentinel for the
// same condition.
var ErrMaxTokensTruncated = errors.New("gemini: response truncated at MAX_TOKENS")

// ErrContentFiltered wraps pipeline.ErrValidation (permanent): a SAFETY
// or RECITATION finishReason means the identical prompt will be blocked
// again on retry.
var ErrContentFiltered = fmt.Errorf("%w: gemini: response blocked by safety/recitation filtering", pipeline.ErrValidation)

// Pricing is per-million-token USD cost, used only for CostUSD reporting.
type Pricing struct {
	InPerMTok  float64
	OutPerMTok float64
}

// Provider talks to the Gemini generateContent API.
type Provider struct {
	APIKey  secretstr.String
	Model   string
	BaseURL string
	Client  *http.Client
	Pricing Pricing
}

func New(apiKey secretstr.String, model string, client *http.Client) *Provider {
	return &Provider{APIKey: apiKey, Model: model, BaseURL: defaultBaseURL, Client: client}
}

func (p *Provider) Name() string { return "gemini-api" }

type part struct {
	Text string `json:"text"`
}

type content struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

type generateContentRequest struct {
	Contents          []content `json:"contents"`
	SystemInstruction *content  `json:"systemInstruction,omitempty"`
	GenerationConfig  struct {
		Temperature     float64 `json:"temperature,omitempty"`
		MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	} `json:"generationConfig,omitempty"`
}

type generateContentChunk struct {
	Candidates []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	// Error is populated instead of Candidates when the API fails after
	// already returning HTTP 200 (rare but documented for streaming).
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (p *Provider) buildRequest(req llm.Request) (generateContentRequest, error) {
	nonce, err := llm.NewNonce()
	if err != nil {
		return generateContentRequest{}, err
	}
	contents := make([]content, 0, len(req.Messages)+1)
	if data := llm.RenderDataBlocks(nonce, req.Data); data != "" {
		contents = append(contents, content{Role: "user", Parts: []part{{Text: data}}})
	}
	for _, m := range req.Messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, content{Role: role, Parts: []part{{Text: m.Text}}})
	}
	out := generateContentRequest{Contents: contents}
	if req.System != "" {
		out.SystemInstruction = &content{Parts: []part{{Text: req.System}}}
	}
	out.GenerationConfig.Temperature = req.Temperature
	out.GenerationConfig.MaxOutputTokens = req.MaxTokens
	return out, nil
}

func (p *Provider) Generate(ctx context.Context, req llm.Request) (llm.Response, error) {
	return p.Stream(ctx, req, nil)
}

func (p *Provider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	start := time.Now()
	body, err := p.buildRequest(req)
	if err != nil {
		return llm.Response{}, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return llm.Response{}, err
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", p.BaseURL, p.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return llm.Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.APIKey.Reveal())

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return llm.Response{}, fmt.Errorf("gemini: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return llm.Response{}, llm.ClassifyHTTP(resp.StatusCode, "gemini", string(b))
	}

	var text bytes.Buffer
	var usage llm.Usage
	var finishReason string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var chunk generateContentChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return llm.Response{}, fmt.Errorf("gemini: decode stream chunk: %w", err)
		}
		if chunk.Error != nil {
			return llm.Response{}, fmt.Errorf("gemini: mid-stream error (%s): %s", chunk.Error.Status, chunk.Error.Message)
		}
		for _, cand := range chunk.Candidates {
			for _, prt := range cand.Content.Parts {
				if prt.Text != "" {
					text.WriteString(prt.Text)
					if onDelta != nil {
						onDelta(llm.Delta{Text: prt.Text})
					}
				}
			}
			if cand.FinishReason != "" {
				finishReason = cand.FinishReason
			}
		}
		if chunk.UsageMetadata.PromptTokenCount != 0 {
			usage.In = chunk.UsageMetadata.PromptTokenCount
		}
		if chunk.UsageMetadata.CandidatesTokenCount != 0 {
			usage.Out = chunk.UsageMetadata.CandidatesTokenCount
		}
	}
	if err := scanner.Err(); err != nil {
		return llm.Response{}, fmt.Errorf("gemini: stream read: %w", err)
	}
	switch finishReason {
	case "":
		return llm.Response{}, ErrStreamIncomplete
	case "STOP":
		// normal completion
	case "MAX_TOKENS":
		return llm.Response{}, fmt.Errorf("%w: stopped at maxOutputTokens=%d", ErrMaxTokensTruncated, req.MaxTokens)
	case "SAFETY", "RECITATION":
		return llm.Response{}, ErrContentFiltered
	default:
		return llm.Response{}, fmt.Errorf("gemini: generation stopped with finishReason %q", finishReason)
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
