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

type fakeProber struct {
	calls    int
	provider string
	ok       bool
	detail   string
}

func (f *fakeProber) Check(_ context.Context, _ uuid.UUID, _ *uuid.UUID, provider string) (bool, string, error) {
	f.calls++
	f.provider = provider
	return f.ok, f.detail, nil
}

func freshWorker(providers map[string]workerstatus.ProviderInfo) fakeWorkerStatus {
	return fakeWorkerStatus{status: workerstatus.Status{Fresh: true, Providers: providers}}
}

func TestTestLLMSettingsRateLimited(t *testing.T) {
	h := &SettingsAPI{
		Store:         &fakeStore{settings: registry.Settings{Default: "claude-cli"}},
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

// The probe runs in the worker, even though this process has no
// claude-cli adapter of its own.
func TestTestLLMSettingsProbesAvailableProviderInTheWorker(t *testing.T) {
	prober := &fakeProber{ok: true}
	h := &SettingsAPI{
		Store:         &fakeStore{settings: registry.Settings{Default: "claude-cli"}},
		Registry:      &registry.Registry{Providers: map[string]llm.Provider{}},
		WorkerStatus:  freshWorker(map[string]workerstatus.ProviderInfo{"claude-cli": {Available: true}}),
		TestRateLimit: fakeRateLimiter{allow: true},
		Probe:         prober,
	}
	resp, err := h.TestLLMSettings(ctxWithTenant(), gen.TestLLMSettingsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := resp.(gen.TestLLMSettings200JSONResponse)
	if !ok || !result.Ok {
		t.Fatalf("expected a successful test result, got %#v", resp)
	}
	if prober.calls != 1 || prober.provider != "claude-cli" {
		t.Fatalf("probe calls=%d provider=%q", prober.calls, prober.provider)
	}
	if result.LatencyMs == nil || *result.LatencyMs < 0 {
		t.Fatalf("a probed provider reports its latency, got %v", result.LatencyMs)
	}
}

func TestTestLLMSettingsReportsProbeFailure(t *testing.T) {
	prober := &fakeProber{ok: false, detail: "claudecli: not logged in"}
	h := &SettingsAPI{
		Store:         &fakeStore{settings: registry.Settings{Default: "claude-cli"}},
		WorkerStatus:  freshWorker(map[string]workerstatus.ProviderInfo{"claude-cli": {Available: true}}),
		TestRateLimit: fakeRateLimiter{allow: true},
		Probe:         prober,
	}
	resp, _ := h.TestLLMSettings(ctxWithTenant(), gen.TestLLMSettingsRequestObject{})
	result := resp.(gen.TestLLMSettings200JSONResponse)
	if result.Ok || result.Detail == nil || *result.Detail != "claudecli: not logged in" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.LatencyMs == nil {
		t.Fatal("a reached but failing provider still reports its latency")
	}
}

// A provider the worker cannot run fails fast with the worker's reason
// instead of enqueuing a probe that would never finish.
func TestTestLLMSettingsFailsFastForUnavailableProvider(t *testing.T) {
	prober := &fakeProber{ok: true}
	h := &SettingsAPI{
		Store: &fakeStore{settings: registry.Settings{Default: "ollama"}},
		WorkerStatus: freshWorker(map[string]workerstatus.ProviderInfo{
			"ollama": {DisabledReason: "no model configured"},
		}),
		TestRateLimit: fakeRateLimiter{allow: true},
		Probe:         prober,
	}
	resp, _ := h.TestLLMSettings(ctxWithTenant(), gen.TestLLMSettingsRequestObject{})
	result := resp.(gen.TestLLMSettings200JSONResponse)
	if result.Ok || result.Detail == nil || *result.Detail != "no model configured" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if prober.calls != 0 {
		t.Fatal("an unavailable provider must not be probed")
	}
}

func TestGetLLMSettingsClaudeCLIAvailabilityComesFromWorkerStatus(t *testing.T) {
	h := &SettingsAPI{
		Store:    &fakeStore{settings: registry.Settings{Default: "claude-cli"}},
		Registry: &registry.Registry{Providers: map[string]llm.Provider{}},
		WorkerStatus: freshWorker(map[string]workerstatus.ProviderInfo{
			"claude-cli": {Available: true},
		}),
	}
	resp, _ := h.GetLLMSettings(ctxWithTenant(), gen.GetLLMSettingsRequestObject{})
	for _, p := range resp.(gen.GetLLMSettings200JSONResponse).Providers {
		switch p.Name {
		case "claude-cli":
			if !p.Available {
				t.Fatal("claude-cli must be available when the worker reports it healthy")
			}
		case "gemini-api":
			if p.Available || p.DisabledReason == nil {
				t.Fatal("gemini-api has no key anywhere and must be unavailable with a reason")
			}
		}
	}
}

type fakeSecretsChecker map[string]bool

func (f fakeSecretsChecker) Configured(_ context.Context, _ string, provider string) (bool, error) {
	return f[provider], nil
}

// A tenant's own key makes a key-based provider available: the worker
// builds that adapter per call from the same encrypted secrets.
func TestGetLLMSettingsTenantKeyMakesProviderAvailable(t *testing.T) {
	h := &SettingsAPI{
		Store: &fakeStore{settings: registry.Settings{Default: "claude-cli"}},
		Registry: &registry.Registry{Factories: map[string]func(string) llm.Provider{
			"anthropic-api": func(string) llm.Provider { return fakeProvider{name: "anthropic-api"} },
		}},
		Secrets:      fakeSecretsChecker{"anthropic-api": true},
		WorkerStatus: freshWorker(map[string]workerstatus.ProviderInfo{}),
	}
	resp, _ := h.GetLLMSettings(ctxWithTenant(), gen.GetLLMSettingsRequestObject{})
	for _, p := range resp.(gen.GetLLMSettings200JSONResponse).Providers {
		if p.Name == "anthropic-api" && !p.Available {
			t.Fatal("anthropic-api with a stored tenant key must be available")
		}
	}
}
