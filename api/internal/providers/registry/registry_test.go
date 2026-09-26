package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
)

type fakeBYOK struct {
	keys map[string]string // provider -> plaintext key
	err  error
}

func (f fakeBYOK) Get(_ context.Context, _ uuid.UUID, provider string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	key, ok := f.keys[provider]
	if !ok {
		return "", pgx.ErrNoRows
	}
	return key, nil
}

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

func TestResolvePrefersTenantBYOKKeyOverProcessWideAdapter(t *testing.T) {
	r := &Registry{
		Store:     fakeStore{settings: Settings{Default: "anthropic-api"}},
		Providers: map[string]llm.Provider{"anthropic-api": fakeProvider{name: "operator-key"}},
		BYOK:      fakeBYOK{keys: map[string]string{"anthropic-api": "tenant-secret"}},
		Factories: map[string]func(string) llm.Provider{
			"anthropic-api": func(key string) llm.Provider { return fakeProvider{name: "byok:" + key} },
		},
	}
	p, name, err := r.Resolve(context.Background(), uuid.New(), ActionOutline)
	if err != nil {
		t.Fatal(err)
	}
	if name != "anthropic-api" {
		t.Fatalf("provider name = %q, want anthropic-api", name)
	}
	if got := p.Name(); got != "byok:tenant-secret" {
		t.Fatalf("expected the BYOK-constructed adapter, got %q", got)
	}
}

func TestResolveFallsBackToProcessWideAdapterWhenTenantHasNoKey(t *testing.T) {
	r := &Registry{
		Store:     fakeStore{settings: Settings{Default: "anthropic-api"}},
		Providers: map[string]llm.Provider{"anthropic-api": fakeProvider{name: "operator-key"}},
		BYOK:      fakeBYOK{keys: map[string]string{}}, // tenant configured nothing
		Factories: map[string]func(string) llm.Provider{
			"anthropic-api": func(key string) llm.Provider { return fakeProvider{name: "byok:" + key} },
		},
	}
	p, _, err := r.Resolve(context.Background(), uuid.New(), ActionOutline)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Name(); got != "operator-key" {
		t.Fatalf("expected the process-wide adapter as fallback, got %q", got)
	}
}

func TestResolveSurfacesARealBYOKLookupError(t *testing.T) {
	r := &Registry{
		Store: fakeStore{settings: Settings{Default: "anthropic-api"}},
		BYOK:  fakeBYOK{err: errors.New("kek unavailable")},
		Factories: map[string]func(string) llm.Provider{
			"anthropic-api": func(key string) llm.Provider { return fakeProvider{name: key} },
		},
	}
	if _, _, err := r.Resolve(context.Background(), uuid.New(), ActionOutline); err == nil {
		t.Fatal("expected a real BYOK lookup error to surface, not be swallowed as unconfigured")
	}
}

func TestResolveNeverConsultsBYOKForNonKeyBasedProviders(t *testing.T) {
	// Ollama and claude-cli are never in Factories; a BYOK lookup for
	// them must never happen even if BYOK is wired.
	r := &Registry{
		Store:     fakeStore{settings: Settings{Default: ProviderOllama}},
		Providers: map[string]llm.Provider{ProviderOllama: fakeProvider{name: "ollama"}},
		BYOK:      fakeBYOK{err: errors.New("must not be called")},
	}
	p, _, err := r.Resolve(context.Background(), uuid.New(), ActionOutline)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "ollama" {
		t.Fatalf("got %q", p.Name())
	}
}
