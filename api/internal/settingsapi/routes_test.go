package settingsapi

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/providers/workerstatus"
	"loomtale/api/internal/tenant"
)

type fakeStore struct {
	settings registry.Settings
	putCalls int
	lastPut  registry.Settings
}

func (f *fakeStore) Get(context.Context, uuid.UUID) (registry.Settings, error) {
	return f.settings, nil
}
func (f *fakeStore) Put(_ context.Context, _ uuid.UUID, s registry.Settings) error {
	f.putCalls++
	f.lastPut = s
	return nil
}

type fakeProvider struct{ name string }

func (f fakeProvider) Name() string { return f.name }
func (f fakeProvider) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Text: "OK"}, nil
}
func (f fakeProvider) Stream(context.Context, llm.Request, func(llm.Delta)) (llm.Response, error) {
	return llm.Response{Text: "OK"}, nil
}

type fakeWorkerStatus struct {
	status workerstatus.Status
	err    error
}

func (f fakeWorkerStatus) Get(context.Context) (workerstatus.Status, error) { return f.status, f.err }

type fakeRateLimiter struct{ allow bool }

func (f fakeRateLimiter) Allow(context.Context, string) (bool, error) { return f.allow, nil }

func ctxWithTenant() context.Context {
	return tenant.WithInfo(context.Background(), tenant.Info{ID: uuid.New(), Role: "owner"})
}

// TestPutLLMSettingsAcceptsEveryKnownProviderRegardlessOfAdapters is the
// direct regression test for H1: PUT must not reject claude-cli/ollama
// just because this process's own Registry.Providers happens to be
// empty (the real-world shipped topology before the sidecar/ollama
// model are configured).
func TestPutLLMSettingsAcceptsEveryKnownProviderRegardlessOfAdapters(t *testing.T) {
	for _, name := range registry.KnownProviders {
		store := &fakeStore{}
		h := &SettingsAPI{
			Store:    store,
			Registry: &registry.Registry{Providers: map[string]llm.Provider{}}, // empty, as in the real default topology
		}
		resp, err := h.PutLLMSettings(ctxWithTenant(), gen.PutLLMSettingsRequestObject{
			Body: &gen.LLMSettingsUpdate{Default: name},
		})
		if err != nil {
			t.Fatalf("provider %q: unexpected error %v", name, err)
		}
		if _, ok := resp.(gen.PutLLMSettings200JSONResponse); !ok {
			t.Fatalf("provider %q: expected 200, got %#v", name, resp)
		}
		if store.putCalls != 1 || store.lastPut.Default != name {
			t.Fatalf("provider %q: expected Store.Put to be called with it, got %+v (calls=%d)", name, store.lastPut, store.putCalls)
		}
	}
}

func TestPutLLMSettingsRejectsUnknownProvider(t *testing.T) {
	store := &fakeStore{}
	h := &SettingsAPI{Store: store, Registry: &registry.Registry{}}
	resp, err := h.PutLLMSettings(ctxWithTenant(), gen.PutLLMSettingsRequestObject{
		Body: &gen.LLMSettingsUpdate{Default: "not-a-real-provider"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.PutLLMSettings400ApplicationProblemPlusJSONResponse); !ok {
		t.Fatalf("expected 400, got %#v", resp)
	}
	if store.putCalls != 0 {
		t.Fatal("Store.Put must not be called for an invalid provider")
	}
}

func TestPutLLMSettingsRejectsUnknownOverrideProvider(t *testing.T) {
	store := &fakeStore{}
	h := &SettingsAPI{Store: store, Registry: &registry.Registry{}}
	bogus := "not-a-real-provider"
	resp, err := h.PutLLMSettings(ctxWithTenant(), gen.PutLLMSettingsRequestObject{
		Body: &gen.LLMSettingsUpdate{Default: "claude-cli", Overrides: &gen.LLMActionOverrides{Draft: &bogus}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.PutLLMSettings400ApplicationProblemPlusJSONResponse); !ok {
		t.Fatalf("expected 400, got %#v", resp)
	}
}

// TestGetLLMSettingsOllamaAvailabilityComesFromWorkerStatus is deviation
// 3's fix applied to GET: Ollama's availability must come from the
// worker's own heartbeat, not this process's (always-empty-for-ollama)
// registry.
func TestGetLLMSettingsOllamaAvailabilityComesFromWorkerStatus(t *testing.T) {
	h := &SettingsAPI{
		Store:    &fakeStore{settings: registry.Settings{Default: "ollama"}},
		Registry: &registry.Registry{Providers: map[string]llm.Provider{}},
		WorkerStatus: fakeWorkerStatus{status: workerstatus.Status{
			Fresh:     true,
			Providers: map[string]workerstatus.ProviderInfo{"ollama": {Available: true}},
		}},
	}
	resp, err := h.GetLLMSettings(ctxWithTenant(), gen.GetLLMSettingsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	settings, ok := resp.(gen.GetLLMSettings200JSONResponse)
	if !ok {
		t.Fatalf("expected 200, got %#v", resp)
	}
	found := false
	for _, p := range settings.Providers {
		if p.Name == "ollama" {
			found = true
			if !p.Available {
				t.Fatal("expected ollama to be reported available from a fresh worker_status row")
			}
		}
	}
	if !found {
		t.Fatal("expected ollama to appear in Providers even though it has no adapter in this process")
	}
}

func TestGetLLMSettingsOllamaUnavailableWhenWorkerStatusStale(t *testing.T) {
	h := &SettingsAPI{
		Store:    &fakeStore{settings: registry.Settings{Default: "ollama"}},
		Registry: &registry.Registry{Providers: map[string]llm.Provider{}},
		WorkerStatus: fakeWorkerStatus{status: workerstatus.Status{
			Fresh:     false,
			Providers: map[string]workerstatus.ProviderInfo{"ollama": {Available: true}},
		}},
	}
	resp, _ := h.GetLLMSettings(ctxWithTenant(), gen.GetLLMSettingsRequestObject{})
	settings := resp.(gen.GetLLMSettings200JSONResponse)
	for _, p := range settings.Providers {
		if p.Name == "ollama" && p.Available {
			t.Fatal("expected ollama to be unavailable when the worker_status row is stale")
		}
	}
}

func TestTestLLMSettingsRateLimited(t *testing.T) {
	h := &SettingsAPI{
		Store:         &fakeStore{settings: registry.Settings{Default: "claude-cli"}},
		Registry:      &registry.Registry{Providers: map[string]llm.Provider{"claude-cli": fakeProvider{name: "claude-cli"}}},
		TestRateLimit: fakeRateLimiter{allow: false},
	}
	resp, err := h.TestLLMSettings(ctxWithTenant(), gen.TestLLMSettingsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.TestLLMSettings429ApplicationProblemPlusJSONResponse); !ok {
		t.Fatalf("expected 429, got %#v", resp)
	}
}

func TestTestLLMSettingsRunsConfiguredProvider(t *testing.T) {
	h := &SettingsAPI{
		Store:         &fakeStore{settings: registry.Settings{Default: "claude-cli"}},
		Registry:      &registry.Registry{Providers: map[string]llm.Provider{"claude-cli": fakeProvider{name: "claude-cli"}}},
		TestRateLimit: fakeRateLimiter{allow: true},
	}
	resp, err := h.TestLLMSettings(ctxWithTenant(), gen.TestLLMSettingsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := resp.(gen.TestLLMSettings200JSONResponse)
	if !ok || !result.Ok {
		t.Fatalf("expected a successful test result, got %#v", resp)
	}
}

func TestTestLLMSettingsReportsUnconfiguredProvider(t *testing.T) {
	h := &SettingsAPI{
		Store:         &fakeStore{settings: registry.Settings{Default: "gemini-api"}},
		Registry:      &registry.Registry{Providers: map[string]llm.Provider{}},
		TestRateLimit: fakeRateLimiter{allow: true},
	}
	resp, err := h.TestLLMSettings(ctxWithTenant(), gen.TestLLMSettingsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	result := resp.(gen.TestLLMSettings200JSONResponse)
	if result.Ok {
		t.Fatal("expected ok=false for an unconfigured provider")
	}
}
