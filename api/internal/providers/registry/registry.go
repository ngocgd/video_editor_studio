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

// Registry resolves a tenant+action to a concrete llm.Provider, and
// implements the queue/model-residency side of the phase 3 contract for
// LLM steps.
type Registry struct {
	Store settingsStore
	// Providers maps a provider name (as stored in llm_settings and
	// secrets) to its constructed adapter. A provider absent from this
	// map (e.g. claude-cli when its self-check failed) is treated as
	// unavailable.
	Providers map[string]llm.Provider
	// OllamaModel is the model name Ollama is configured to serve; used
	// to build the ModelRef phase 3 needs for GPU residency. Empty until
	// phase 9c seeds a model.
	OllamaModel string
}

// ErrProviderNotConfigured is returned when the resolved provider name
// has no constructed adapter (missing API key, self-check failure, etc).
var ErrProviderNotConfigured = errors.New("registry: provider not configured")

// Resolve returns the concrete provider for tenantID+action.
func (r *Registry) Resolve(ctx context.Context, tenantID uuid.UUID, action Action) (llm.Provider, string, error) {
	settings, err := r.Store.Get(ctx, tenantID)
	if err != nil {
		return nil, "", err
	}
	name := settings.ProviderFor(action)
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
