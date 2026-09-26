package analytics

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/registry"
)

// Explain run and step identity. The step runs in the worker, which
// holds every provider adapter and the GPU residency Ollama needs.
const (
	KindExplain         = "analytics.explain"
	RunKindExplain      = "analytics.explain"
	ScopeKindChannel    = "youtube_channel"
	explainMaxTokens    = 700
	explainTemperature  = 0.3
	explainAction       = registry.ActionSummary
	explainDataBlockTag = "channel_analytics"
)

// explainSystem is a fixed server template: the only variable content is
// the aggregates, passed as a data block.
const explainSystem = `You are an analyst for a small YouTube channel. The data block holds the channel's aggregated numbers as JSON: totals for the window and the window before it, progress towards the YouTube Partner Program (4,000 public watch hours in 12 months and 1,000 subscribers), per-video totals (ctr is a fraction, averageViewPercentage is a percentage) and the findings of fixed rules with their evidence. A null value means YouTube did not provide that metric: say so instead of guessing.
Explain in plain English, in at most 6 short paragraphs of plain text (no Markdown, no HTML, no links): what changed between the two windows, which videos stand out and why, what each finding means, and the two or three most useful next steps. Refer to videos by their videoId. Use only the numbers in the data block.`

// LLMResolver is the part of the provider registry the step uses.
type LLMResolver interface {
	Resolve(ctx context.Context, tenantID uuid.UUID, action registry.Action) (llm.Provider, string, error)
	QueueFor(ctx context.Context, tenantID uuid.UUID, action registry.Action) (string, error)
	ModelRefFor(providerName string) *pipeline.ModelRef
}

// ExplainHandler is the analytics.explain pipeline step: it sends the
// step's ExplainInput to the tenant's summary provider and stores the
// plain-text answer with the provider, model and cost in its output.
type ExplainHandler struct {
	Registry LLMResolver
}

var _ pipeline.StepHandler = (*ExplainHandler)(nil)

// Kind implements pipeline.StepHandler.
func (h *ExplainHandler) Kind() string { return KindExplain }

// Queue follows the summary provider: Ollama on the GPU queue, others on
// the LLM queue.
func (h *ExplainHandler) Queue(ctx context.Context, s pipeline.StepRef) (string, error) {
	return h.Registry.QueueFor(ctx, s.TenantID, explainAction)
}

// InputHash is unique per step: an explanation is never reused.
func (h *ExplainHandler) InputHash(_ context.Context, s pipeline.StepRef) (string, error) {
	return KindExplain + ":" + s.ID.String(), nil
}

// ModelRef reports GPU residency only when the provider is Ollama.
func (h *ExplainHandler) ModelRef(ctx context.Context, s pipeline.StepRef) (*pipeline.ModelRef, error) {
	_, name, err := h.Registry.Resolve(ctx, s.TenantID, explainAction)
	if err != nil {
		return nil, nil // Run surfaces the real error
	}
	return h.Registry.ModelRefFor(name), nil
}

// Run implements pipeline.StepHandler.
func (h *ExplainHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	var in ExplainInput
	if err := sc.Input(&in); err != nil {
		return nil, fmt.Errorf("%w: analytics: explain input: %v", pipeline.ErrValidation, err)
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	provider, name, err := h.Registry.Resolve(ctx, sc.Tenant(), explainAction)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	resp, err := provider.Generate(ctx, llm.Request{
		System:      explainSystem,
		Messages:    []llm.Message{{Role: "user", Text: "Explain these channel analytics."}},
		Data:        []llm.DataBlock{{Label: explainDataBlockTag, Text: string(data), Origin: llm.OriginDerived}},
		MaxTokens:   explainMaxTokens,
		Temperature: explainTemperature,
	})
	if err != nil {
		return nil, err
	}
	sc.Progress(100, 0)
	return ExplainOutput(resp, name), nil
}

// ExplainOutput is the step output for a provider response.
func ExplainOutput(resp llm.Response, providerName string) pipeline.Output {
	provider := resp.Provider
	if provider == "" {
		provider = providerName
	}
	return pipeline.Output{"text": resp.Text, "provider": provider, "model": resp.Model, "costUsd": resp.CostUSD}
}

// ExplainResult is a decoded explain step output.
type ExplainResult struct {
	Text     string  `json:"text"`
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	CostUSD  float64 `json:"costUsd"`
}

// DecodeExplainOutput reads a finished step's output.
func DecodeExplainOutput(raw []byte) (ExplainResult, error) {
	var r ExplainResult
	if len(raw) == 0 {
		return r, nil
	}
	err := json.Unmarshal(raw, &r)
	return r, err
}
