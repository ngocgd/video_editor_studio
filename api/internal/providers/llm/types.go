// Package llm defines the provider-agnostic LLM interface every adapter
// (Ollama, claude-cli, anthropic-api, gemini-api) implements. Callers
// (phase 6+) only ever depend on Provider, never a concrete adapter.
package llm

import (
	"context"
	"time"
)

// DataOrigin classifies where a DataBlock's content came from, and drives
// taint propagation (see Tainted below).
type DataOrigin string

const (
	OriginUser    DataOrigin = "user"
	OriginImport  DataOrigin = "import"
	OriginDerived DataOrigin = "derived"
	OriginModel   DataOrigin = "model"
)

// DataBlock is one piece of untrusted or derived story content included
// in a request. It is never concatenated into System (which holds only
// fixed server templates); Render wraps it in a nonce-fenced tag so a
// provider cannot be tricked into treating its content as instructions.
type DataBlock struct {
	Label   string
	Text    string
	Origin  DataOrigin
	Tainted bool
}

// Message is one turn in the conversation sent to the provider.
type Message struct {
	Role string // "user" or "assistant"
	Text string
}

// Request is the provider-agnostic input to Generate/Stream.
type Request struct {
	System      string // fixed server template only, never user content
	Messages    []Message
	Data        []DataBlock
	MaxTokens   int
	Temperature float64
	// JSONSchema, if non-empty, is a JSON Schema the response text must
	// validate against (see ValidateJSON); Generate/Stream retry once on
	// a validation failure with a corrective follow-up message.
	JSONSchema string
}

// Usage reports token counts for cost accounting.
type Usage struct {
	In  int
	Out int
}

// Response is the provider-agnostic output of Generate/Stream.
type Response struct {
	Text     string
	Usage    Usage
	CostUSD  float64
	Model    string
	Provider string
	Duration time.Duration
}

// Delta is one incremental piece of a streamed response.
type Delta struct {
	Text string
}

// Provider is implemented by every LLM adapter. No provider is ever given
// tool access; every request renders untrusted content through
// RenderDataBlocks first.
type Provider interface {
	Name() string
	Generate(ctx context.Context, req Request) (Response, error)
	Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error)
}

// ErrProviderDisabled is returned by a provider whose adapter is
// unavailable in the current mode (e.g. claude-cli when APP_MODE=saas) or
// whose startup self-check failed.
type ErrProviderDisabled struct {
	Provider string
	Reason   string
}

func (e *ErrProviderDisabled) Error() string {
	return "llm: provider " + e.Provider + " disabled: " + e.Reason
}
