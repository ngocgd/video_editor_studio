// Package claudecli implements llm.Provider by calling the isolated
// llm-cli sidecar (api/cmd/llmcli) over its internal, bearer-token
// authenticated HTTP endpoint. It never spawns the claude CLI itself and
// never sees the OAuth token: those live only in the sidecar container.
package claudecli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/secretstr"
)

// Provider calls the llm-cli sidecar's POST /v1/run.
type Provider struct {
	BaseURL     string
	BearerToken secretstr.String
	Client      *http.Client
	// SaaSMode disables this provider entirely (local-only, per the
	// contract): callers must check Disabled before constructing a
	// request in APP_MODE=saas rather than relying on a runtime error.
	SaaSMode bool
}

// New builds a claude-cli adapter pointed at the sidecar's base URL
// (e.g. http://llm-cli:8090 on llm_net).
func New(baseURL string, bearerToken secretstr.String, client *http.Client, saasMode bool) *Provider {
	return &Provider{BaseURL: baseURL, BearerToken: bearerToken, Client: client, SaaSMode: saasMode}
}

func (p *Provider) Name() string { return "claude-cli" }

// Disabled reports whether this provider must never be used, per the
// contract: claude-cli is local-only and disabled entirely in SaaS mode.
func (p *Provider) Disabled() error {
	if p.SaaSMode {
		return &llm.ErrProviderDisabled{Provider: p.Name(), Reason: "claude-cli is local-only and disabled when APP_MODE=saas"}
	}
	return nil
}

type runRequestBody struct {
	Prompt string `json:"prompt"`
}

type streamLine struct {
	Type      string  `json:"type"`
	Text      string  `json:"text,omitempty"`
	IsError   bool    `json:"is_error,omitempty"`
	Error     string  `json:"error,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
	InTokens  int     `json:"in_tokens,omitempty"`
	OutTokens int     `json:"out_tokens,omitempty"`
}

// promptText flattens System (never sent; the sidecar's system prompt is
// fixed server-side) and the rendered data blocks plus conversation
// messages into the single stdin prompt the CLI expects.
func promptText(req llm.Request) (string, error) {
	nonce, err := llm.NewNonce()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if data := llm.RenderDataBlocks(nonce, req.Data); data != "" {
		buf.WriteString(data)
		buf.WriteString("\n")
	}
	for _, m := range req.Messages {
		buf.WriteString(m.Role)
		buf.WriteString(": ")
		buf.WriteString(m.Text)
		buf.WriteString("\n")
	}
	return buf.String(), nil
}

func (p *Provider) Generate(ctx context.Context, req llm.Request) (llm.Response, error) {
	return p.Stream(ctx, req, nil)
}

func (p *Provider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	if err := p.Disabled(); err != nil {
		return llm.Response{}, err
	}
	start := time.Now()
	prompt, err := promptText(req)
	if err != nil {
		return llm.Response{}, err
	}

	payload, err := json.Marshal(runRequestBody{Prompt: prompt})
	if err != nil {
		return llm.Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/v1/run", bytes.NewReader(payload))
	if err != nil {
		return llm.Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.BearerToken.Reveal())

	resp, err := p.Client.Do(httpReq)
	if err != nil {
		return llm.Response{}, fmt.Errorf("claudecli: sidecar request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return llm.Response{}, fmt.Errorf("claudecli: sidecar returned status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var final streamLine
	for scanner.Scan() {
		var line streamLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			return llm.Response{}, fmt.Errorf("claudecli: decode sidecar line: %w", err)
		}
		switch line.Type {
		case "delta":
			if onDelta != nil {
				onDelta(llm.Delta{Text: line.Text})
			}
		case "result":
			final = line
		}
	}
	if err := scanner.Err(); err != nil {
		return llm.Response{}, fmt.Errorf("claudecli: read sidecar stream: %w", err)
	}
	if final.IsError {
		return llm.Response{}, fmt.Errorf("claudecli: %s", final.Error)
	}

	return llm.Response{
		Text:     final.Text,
		Usage:    llm.Usage{In: final.InTokens, Out: final.OutTokens},
		CostUSD:  final.CostUSD,
		Model:    "claude-cli",
		Provider: p.Name(),
		Duration: time.Since(start),
	}, nil
}
