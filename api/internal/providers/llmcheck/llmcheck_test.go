package llmcheck

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/llm/anthropic"
	"loomtale/api/internal/providers/registry"
)

type fakeProvider struct{ err error }

func (f fakeProvider) Name() string { return "fake" }
func (f fakeProvider) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Text: "OK"}, f.err
}
func (f fakeProvider) Stream(context.Context, llm.Request, func(llm.Delta)) (llm.Response, error) {
	return llm.Response{Text: "OK"}, f.err
}

func TestHandlersCoverEveryKnownProviderAndRouteOllamaToGPU(t *testing.T) {
	reg := &registry.Registry{OllamaModel: "qwen"}
	handlers := Handlers(reg)
	if len(handlers) != len(registry.KnownProviders) {
		t.Fatalf("got %d handlers, want %d", len(handlers), len(registry.KnownProviders))
	}
	for _, h := range handlers {
		queue, err := h.Queue(context.Background(), pipeline.StepRef{})
		if err != nil {
			t.Fatal(err)
		}
		ref, _ := h.ModelRef(context.Background(), pipeline.StepRef{})
		if h.Provider == registry.ProviderOllama {
			if queue != pipeline.QueueGPU || ref == nil {
				t.Fatalf("ollama probe: queue=%s modelRef=%v", queue, ref)
			}
			continue
		}
		if queue != pipeline.QueueLLM || ref != nil {
			t.Fatalf("%s probe: queue=%s modelRef=%v", h.Provider, queue, ref)
		}
		if h.Kind() != "llm.check."+h.Provider {
			t.Fatalf("kind = %s", h.Kind())
		}
	}
}

func TestProbeFailuresArePermanentAndKeepTheProviderMessage(t *testing.T) {
	tenantID := uuid.New()
	missing := Probe(context.Background(), &registry.Registry{Providers: map[string]llm.Provider{}}, tenantID, "gemini-api")
	if class, _ := pipeline.Classify(missing); class != pipeline.ClassPermanent {
		t.Fatalf("a missing provider must fail permanently, got class %v (%v)", class, missing)
	}

	failing := Probe(context.Background(), &registry.Registry{Providers: map[string]llm.Provider{
		"claude-cli": fakeProvider{err: errors.New("claudecli: not logged in")},
	}}, tenantID, "claude-cli")
	if class, _ := pipeline.Classify(failing); class != pipeline.ClassPermanent {
		t.Fatalf("a provider error must fail permanently, got class %v", class)
	}
	if got := FailureDetail(failing.Error()); got != "claudecli: not logged in" {
		t.Fatalf("FailureDetail = %q", got)
	}

	if err := Probe(context.Background(), &registry.Registry{Providers: map[string]llm.Provider{
		"anthropic-api": fakeProvider{err: anthropic.ErrMaxTokensTruncated},
	}}, tenantID, "anthropic-api"); err != nil {
		t.Fatalf("a reply cut at the probe's token cap is still a working provider: %v", err)
	}
}

func TestAwaitReturnsTerminalStatusOrTimesOut(t *testing.T) {
	calls := 0
	status, msg, err := Await(context.Background(), func(context.Context) (string, string, error) {
		calls++
		if calls < 3 {
			return "running", "", nil
		}
		return "failed", "boom", nil
	})
	if err != nil || status != "failed" || msg != "boom" {
		t.Fatalf("status=%s msg=%s err=%v", status, msg, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, _, err := Await(ctx, func(context.Context) (string, string, error) { return "queued", "", nil }); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}
