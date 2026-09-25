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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"loomtale/api/internal/secretstr"

	"loomtale/api/internal/providers/llm"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com"

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
		Content content `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
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
		return llm.Response{}, fmt.Errorf("gemini: unexpected status %d: %s", resp.StatusCode, b)
	}

	var text bytes.Buffer
	var usage llm.Usage
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
			continue
		}
		for _, cand := range chunk.Candidates {
			for _, prt := range cand.Content.Parts {
				if prt.Text == "" {
					continue
				}
				text.WriteString(prt.Text)
				if onDelta != nil {
					onDelta(llm.Delta{Text: prt.Text})
				}
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
