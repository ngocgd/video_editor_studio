// Package registry resolves which LLM provider a tenant uses for a given
// action, backed by the llm_settings table, and maps that resolution onto
// the phase 3 queue/model-residency contract (pipeline.StepHandler). It is
// consumed by phase 6's LLM step handlers, never called directly by any
// HTTP route.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
)

// Action names the llm_settings.action_overrides keys the contract
// defines.
type Action string

const (
	ActionOutline    Action = "outline"
	ActionDraft      Action = "draft"
	ActionRewrite    Action = "rewrite"
	ActionTranslate  Action = "translate"
	ActionSceneSplit Action = "scene_split"
	ActionSummary    Action = "summary"
)

// ProviderOllama is the one provider name that resolves to the gpu queue
// and carries a model residency requirement; every other provider name
// resolves to the llm queue with no GPU involvement.
const ProviderOllama = "ollama"

// KnownProviders is the fixed set of provider names the settings API
// accepts, independent of whether this process happens to have a
// constructed adapter for one right now (e.g. Ollama's adapter only
// exists once OLLAMA_MODEL is set in phase 9b, but "ollama" is always a
// valid value to select as a default or override).
var KnownProviders = []string{ProviderOllama, "claude-cli", "anthropic-api", "gemini-api"}

// IsKnownProvider reports whether name is one of KnownProviders.
func IsKnownProvider(name string) bool {
	for _, p := range KnownProviders {
		if p == name {
			return true
		}
	}
	return false
}

// Settings is a tenant's resolved LLM provider configuration.
type Settings struct {
	Default   string
	Overrides map[string]string
}

// ProviderFor returns the provider name for action: the per-action
// override if set, otherwise the tenant default.
func (s Settings) ProviderFor(action Action) string {
	if p, ok := s.Overrides[string(action)]; ok && p != "" {
		return p
	}
	return s.Default
}

// Store persists Settings in the llm_settings table.
type Store struct {
	Queries *dbgen.Queries
	// FallbackDefault is used when a tenant has no llm_settings row yet
	// (env-provided; per the contract "Until phase 9c seeds Ollama, the
	// default is claude-cli").
	FallbackDefault string
}

// Get returns tenant's settings, or {Default: FallbackDefault} if the
// tenant has never configured one.
func (st *Store) Get(ctx context.Context, tenantID uuid.UUID) (Settings, error) {
	row, err := st.Queries.GetLLMSettings(ctx, idconv.ToPg(tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{Default: st.FallbackDefault, Overrides: map[string]string{}}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("registry: load llm_settings: %w", err)
	}
	overrides := map[string]string{}
	if len(row.ActionOverrides) > 0 {
		if err := json.Unmarshal(row.ActionOverrides, &overrides); err != nil {
			return Settings{}, fmt.Errorf("registry: decode action_overrides: %w", err)
		}
	}
	return Settings{Default: row.DefaultProvider, Overrides: overrides}, nil
}

// Put persists s for tenantID (PUT /settings/llm).
func (st *Store) Put(ctx context.Context, tenantID uuid.UUID, s Settings) error {
	overrides, err := json.Marshal(s.Overrides)
	if err != nil {
		return err
	}
	_, err = st.Queries.UpsertLLMSettings(ctx, dbgen.UpsertLLMSettingsParams{
		TenantID:        idconv.ToPg(tenantID),
		DefaultProvider: s.Default,
		ActionOverrides: overrides,
	})
	if err != nil {
		return fmt.Errorf("registry: save llm_settings: %w", err)
	}
	return nil
}

// settingsStore is the subset of *Store that Registry depends on,
// narrowed to an interface so Resolve/QueueFor are testable against a
// fake without a database.
type settingsStore interface {
	Get(ctx context.Context, tenantID uuid.UUID) (Settings, error)
}

var _ settingsStore = (*Store)(nil)

// byokReader is the subset of *secrets.Store Registry needs to resolve a
// tenant's own bring-your-own-key credential. It returns
// pgx.ErrNoRows (wrapped) when the tenant has not configured a key for
// provider, which Resolve treats as "fall through to the process-wide
// adapter", not as an error.
type byokReader interface {
	Get(ctx context.Context, tenantID uuid.UUID, provider string) (string, error)
}

// Registry resolves a tenant+action to a concrete llm.Provider, and
// implements the queue/model-residency side of the phase 3 contract for
// LLM steps.
type Registry struct {
	Store settingsStore
	// Providers maps a provider name (as stored in llm_settings and
	// secrets) to its constructed, process-wide adapter (an
	// operator-provisioned key, local mode only). A provider absent from
	// this map (e.g. claude-cli when its self-check failed) is only
	// unavailable if BYOK/Factories also has nothing for it.
	Providers map[string]llm.Provider
	// OllamaModel is the model name Ollama is configured to serve; used
	// to build the ModelRef phase 3 needs for GPU residency. Empty until
	// phase 9c seeds a model.
	OllamaModel string
	// BYOK, when set, is consulted before Providers for any name present
	// in Factories: a tenant's own key always takes priority over a
	// process-wide operator key for the same provider name.
	BYOK byokReader
	// Factories builds a fresh adapter from a tenant's plaintext API key
	// for a BYOK-eligible provider name (anthropic-api, gemini-api).
	// Ollama and claude-cli are never BYOK: they are reached by URL/local
	// auth, not a bearer key, so they are never keys in this map.
	Factories map[string]func(apiKey string) llm.Provider
}

// ErrProviderNotConfigured is returned when the resolved provider name
// has no constructed adapter (missing API key, self-check failure, etc).
var ErrProviderNotConfigured = errors.New("registry: provider not configured")

// Resolve returns the concrete provider for tenantID+action. A tenant's
// own BYOK key (if the provider supports one and the tenant configured
// one) always wins over the process-wide Providers adapter for the same
// name, so a SaaS tenant is never silently billed against, or limited
// by, another tenant's or the operator's own credential.
func (r *Registry) Resolve(ctx context.Context, tenantID uuid.UUID, action Action) (llm.Provider, string, error) {
	settings, err := r.Store.Get(ctx, tenantID)
	if err != nil {
		return nil, "", err
	}
	name := settings.ProviderFor(action)

	if r.BYOK != nil && r.Factories != nil {
		if factory, ok := r.Factories[name]; ok {
			key, err := r.BYOK.Get(ctx, tenantID, name)
			switch {
			case err == nil && key != "":
				return factory(key), name, nil
			case err != nil && !errors.Is(err, pgx.ErrNoRows):
				return nil, name, fmt.Errorf("registry: byok lookup for %q: %w", name, err)
			}
			// pgx.ErrNoRows (or an empty key): the tenant has not
			// configured their own key for this provider; fall through
			// to the process-wide adapter below.
		}
	}

	p, ok := r.Providers[name]
	if !ok {
		return nil, name, fmt.Errorf("%w: %q", ErrProviderNotConfigured, name)
	}
	return p, name, nil
}

// QueueFor implements the queue half of pipeline.StepHandler for an LLM
// step: Ollama resolves to the gpu queue (it needs residency), every
// other provider resolves to the llm queue.
func (r *Registry) QueueFor(ctx context.Context, tenantID uuid.UUID, action Action) (string, error) {
	settings, err := r.Store.Get(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if settings.ProviderFor(action) == ProviderOllama {
		return pipeline.QueueGPU, nil
	}
	return pipeline.QueueLLM, nil
}

// ModelRefFor implements the residency half of pipeline.StepHandler for
// an LLM step: nil for every provider except Ollama, whose ModelRef
// drives GPUExecutor.ensureModel/residency.Ensure.
func (r *Registry) ModelRefFor(providerName string) *pipeline.ModelRef {
	if providerName != ProviderOllama || r.OllamaModel == "" {
		return nil
	}
	return &pipeline.ModelRef{Backend: ProviderOllama, Model: r.OllamaModel}
}
