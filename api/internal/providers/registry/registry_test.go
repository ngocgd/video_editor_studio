package registry

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
)

type fakeStore struct {
	settings Settings
}

func (f fakeStore) Get(context.Context, uuid.UUID) (Settings, error) { return f.settings, nil }

type fakeProvider struct{ name string }

func (f fakeProvider) Name() string { return f.name }
func (f fakeProvider) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}
func (f fakeProvider) Stream(context.Context, llm.Request, func(llm.Delta)) (llm.Response, error) {
	return llm.Response{}, nil
}

func TestSettingsProviderForUsesOverrideWhenPresent(t *testing.T) {
	s := Settings{Default: "claude-cli", Overrides: map[string]string{"draft": "gemini-api"}}
	if got := s.ProviderFor(ActionDraft); got != "gemini-api" {
		t.Fatalf("ProviderFor(draft) = %q", got)
	}
	if got := s.ProviderFor(ActionOutline); got != "claude-cli" {
		t.Fatalf("ProviderFor(outline) = %q, want default", got)
	}
}

func TestResolveReturnsConfiguredProvider(t *testing.T) {
	r := &Registry{
		Store:     fakeStore{settings: Settings{Default: "claude-cli"}},
		Providers: map[string]llm.Provider{"claude-cli": fakeProvider{name: "claude-cli"}},
	}
	p, name, err := r.Resolve(context.Background(), uuid.New(), ActionOutline)
	if err != nil {
		t.Fatal(err)
	}
	if name != "claude-cli" || p.Name() != "claude-cli" {
		t.Fatalf("got provider %q", name)
	}
}

func TestResolveErrorsOnUnconfiguredProvider(t *testing.T) {
	r := &Registry{
		Store:     fakeStore{settings: Settings{Default: "gemini-api"}},
		Providers: map[string]llm.Provider{"claude-cli": fakeProvider{name: "claude-cli"}},
	}
	if _, _, err := r.Resolve(context.Background(), uuid.New(), ActionOutline); err == nil {
		t.Fatal("expected error for a provider with no constructed adapter")
	}
}

func TestQueueForRoutesOllamaToGPUAndOthersToLLM(t *testing.T) {
	ollamaReg := &Registry{Store: fakeStore{settings: Settings{Default: ProviderOllama}}}
	q, err := ollamaReg.QueueFor(context.Background(), uuid.New(), ActionDraft)
	if err != nil {
		t.Fatal(err)
	}
	if q != pipeline.QueueGPU {
		t.Fatalf("ollama queue = %q, want gpu", q)
	}

	cloudReg := &Registry{Store: fakeStore{settings: Settings{Default: "claude-cli"}}}
	q, err = cloudReg.QueueFor(context.Background(), uuid.New(), ActionDraft)
	if err != nil {
		t.Fatal(err)
	}
	if q != pipeline.QueueLLM {
		t.Fatalf("claude-cli queue = %q, want llm", q)
	}
}

func TestModelRefForOnlyReturnsForOllamaWithModelConfigured(t *testing.T) {
	r := &Registry{OllamaModel: "llama3"}
	if ref := r.ModelRefFor("claude-cli"); ref != nil {
		t.Fatalf("expected nil ModelRef for claude-cli, got %+v", ref)
	}
	if ref := r.ModelRefFor(ProviderOllama); ref == nil || ref.Model != "llama3" {
		t.Fatalf("expected ollama ModelRef, got %+v", ref)
	}

	noModel := &Registry{}
	if ref := noModel.ModelRefFor(ProviderOllama); ref != nil {
		t.Fatalf("expected nil ModelRef when no Ollama model is configured, got %+v", ref)
	}
}
