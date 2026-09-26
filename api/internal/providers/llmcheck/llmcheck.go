// Package llmcheck runs the Settings > LLM "Test" probe as a pipeline
// step, so it executes in the worker: the only process that holds every
// provider adapter the tenant's real actions use and the only one that
// can reach the llm-cli sidecar on llm_net. The api enqueues the step
// and waits briefly for its terminal state.
package llmcheck

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/llm/anthropic"
	"loomtale/api/internal/providers/llm/gemini"
	"loomtale/api/internal/providers/registry"
)

// KindPrefix starts every probe step kind; the provider name follows it,
// e.g. "llm.check.claude-cli". The provider is part of the kind because
// Queue and ModelRef only see a StepRef, not the step's input, and an
// Ollama probe must go through the GPU queue and residency like any
// other Ollama call.
const KindPrefix = "llm.check."

// RunKind is the pipeline run kind a probe is enqueued under.
const RunKind = "llm.check"

// ScopeKind is the scope of a probe run: the tenant itself.
const ScopeKind = "tenant"

// maxTokens hard-caps the probe's reply.
const maxTokens = 8

// probePrompt is a fixed server string: a probe never sends tenant
// content to a provider.
const probePrompt = "Reply with a single word: OK."

// Kind returns the probe step kind for provider.
func Kind(provider string) string { return KindPrefix + provider }

// Handler probes one named provider.
type Handler struct {
	Provider string
	Registry *registry.Registry
}

var _ pipeline.StepHandler = (*Handler)(nil)

// Handlers returns one probe handler per known provider.
func Handlers(reg *registry.Registry) []*Handler {
	out := make([]*Handler, 0, len(registry.KnownProviders))
	for _, name := range registry.KnownProviders {
		out = append(out, &Handler{Provider: name, Registry: reg})
	}
	return out
}

func (h *Handler) Kind() string { return Kind(h.Provider) }

// Queue sends an Ollama probe to the GPU queue (it needs residency) and
// every other provider to the LLM queue, matching registry.QueueFor.
func (h *Handler) Queue(context.Context, pipeline.StepRef) (string, error) {
	if h.Provider == registry.ProviderOllama {
		return pipeline.QueueGPU, nil
	}
	return pipeline.QueueLLM, nil
}

// InputHash is unique per step: a probe is never reused or marked stale.
func (h *Handler) InputHash(_ context.Context, s pipeline.StepRef) (string, error) {
	return fmt.Sprintf("%s:%s", h.Kind(), s.ID), nil
}

func (h *Handler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return h.Registry.ModelRefFor(h.Provider), nil
}

// Run calls the provider once with the fixed probe prompt.
func (h *Handler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if err := Probe(ctx, h.Registry, sc.Tenant(), h.Provider); err != nil {
		return nil, err
	}
	return pipeline.Output{"provider": h.Provider, "ok": true}, nil
}

// Probe resolves provider for tenantID and sends the probe prompt. Any
// failure is wrapped as permanent: the owner is waiting on the answer,
// so a retry with backoff would only turn a clear failure into a timeout.
func Probe(ctx context.Context, reg *registry.Registry, tenantID uuid.UUID, provider string) error {
	p, err := reg.ResolveName(ctx, tenantID, provider)
	if err != nil {
		return fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	if _, err := p.Generate(ctx, llm.Request{
		Messages:  []llm.Message{{Role: "user", Text: probePrompt}},
		MaxTokens: maxTokens,
	}); err != nil && !truncated(err) {
		return fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	return nil
}

// FailureDetail strips the permanent-failure marker Probe adds, so the
// owner sees the provider's own message.
func FailureDetail(errorMsg string) string {
	return strings.TrimPrefix(errorMsg, pipeline.ErrValidation.Error()+": ")
}

// pollInterval is how often Await re-reads the probe step.
const pollInterval = 250 * time.Millisecond

// StepReader reads one step's status and error message.
type StepReader func(ctx context.Context) (status, errorMsg string, err error)

// ErrTimeout is returned by Await when the step does not finish in time.
var ErrTimeout = errors.New("llmcheck: the worker did not finish the probe in time")

// Await polls read until the step is terminal or ctx ends. It returns
// the terminal status and error message.
func Await(ctx context.Context, read StepReader) (status, errorMsg string, err error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		status, errorMsg, err = read(ctx)
		if err != nil {
			return "", "", err
		}
		switch status {
		case "done", "failed", "canceled":
			return status, errorMsg, nil
		}
		select {
		case <-ctx.Done():
			return status, "", ErrTimeout
		case <-ticker.C:
		}
	}
}

// truncated reports a reply cut at the probe's tiny token cap: the
// provider answered, which is all the probe needs to know.
func truncated(err error) bool {
	return errors.Is(err, anthropic.ErrMaxTokensTruncated) || errors.Is(err, gemini.ErrMaxTokensTruncated)
}
