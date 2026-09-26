// Package workerstatus is the cross-process status channel between the
// Go worker (the only process that can reach comfyui/ollama/pyworker,
// since gpu_net is internal-only) and the api process (which cannot).
// The worker upserts its own row every 5s and immediately after any
// residency change; the api process reads the freshest row instead of
// holding a network path it architecturally does not have.
package workerstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// StaleAfter is how old a row can be before Get reports Fresh=false. The
// worker's own heartbeat interval is 5s, so 15s tolerates a couple of
// missed beats before declaring it offline.
const StaleAfter = 15 * time.Second

// GPU mirrors the fields of pipeline.GpuSnapshot this package needs to
// round-trip through JSON without importing pipeline (kept a plain,
// storage-shaped struct rather than reusing pipeline's type directly, so
// a future field added to GpuSnapshot for in-process use does not
// silently change this on-disk contract).
type GPU struct {
	Present         bool      `json:"present"`
	TotalMB         int64     `json:"total_mb"`
	FreeMB          int64     `json:"free_mb"`
	BudgetMB        int64     `json:"budget_mb"`
	RenderReserveMB int64     `json:"render_reserve_mb"`
	MeasuredAt      time.Time `json:"measured_at"`
}

// ProviderInfo is one provider's availability as observed by the
// process that actually constructs/calls it.
type ProviderInfo struct {
	Available      bool   `json:"available"`
	DisabledReason string `json:"disabled_reason,omitempty"`
	// Loaded lists the models the backend itself reports as resident
	// (ComfyUI /system_stats, Ollama /api/ps, pyworker ListEngines).
	Loaded []string `json:"loaded,omitempty"`
}

// Status is one worker's full reported state.
type Status struct {
	WorkerID    string
	GPU         GPU
	ResidentRef string
	Providers   map[string]ProviderInfo
	UpdatedAt   time.Time
	// Fresh is UpdatedAt within StaleAfter of time.Now(); Get sets this,
	// callers should treat a non-fresh row as worker_offline rather than
	// trusting stale data.
	Fresh bool
}

// Store reads/writes worker_status.
type Store struct {
	Queries *dbgen.Queries
}

// Upsert writes workerID's current status. Called on a heartbeat timer
// and immediately after any residency change.
func (s *Store) Upsert(ctx context.Context, workerID string, gpu GPU, residentRef string, providers map[string]ProviderInfo) error {
	gpuJSON, err := json.Marshal(gpu)
	if err != nil {
		return fmt.Errorf("workerstatus: marshal gpu: %w", err)
	}
	providersJSON, err := json.Marshal(providers)
	if err != nil {
		return fmt.Errorf("workerstatus: marshal providers: %w", err)
	}
	err = s.Queries.UpsertWorkerStatus(ctx, dbgen.UpsertWorkerStatusParams{
		WorkerID:    workerID,
		Gpu:         gpuJSON,
		ResidentRef: idconv.ToPgText(residentRef),
		Providers:   providersJSON,
	})
	if err != nil {
		return fmt.Errorf("workerstatus: upsert: %w", err)
	}
	return nil
}

// ErrNoStatus is returned when no worker has ever reported a status row
// (a fresh install before the worker's first heartbeat).
var ErrNoStatus = errors.New("workerstatus: no worker has reported status yet")

// Get returns the single freshest row (there is one physical GPU and one
// active worker process in this deployment shape, so "latest by
// updated_at" is simply "the worker").
func (s *Store) Get(ctx context.Context) (Status, error) {
	row, err := s.Queries.GetLatestWorkerStatus(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return Status{}, ErrNoStatus
	}
	if err != nil {
		return Status{}, fmt.Errorf("workerstatus: get: %w", err)
	}

	var gpu GPU
	if len(row.Gpu) > 0 {
		if err := json.Unmarshal(row.Gpu, &gpu); err != nil {
			return Status{}, fmt.Errorf("workerstatus: decode gpu: %w", err)
		}
	}
	providers := map[string]ProviderInfo{}
	if len(row.Providers) > 0 {
		if err := json.Unmarshal(row.Providers, &providers); err != nil {
			return Status{}, fmt.Errorf("workerstatus: decode providers: %w", err)
		}
	}
	updatedAt := idconv.FromPgTimestamptz(row.UpdatedAt)
	return Status{
		WorkerID:    row.WorkerID,
		GPU:         gpu,
		ResidentRef: row.ResidentRef.String,
		Providers:   providers,
		UpdatedAt:   updatedAt,
		Fresh:       time.Since(updatedAt) <= StaleAfter,
	}, nil
}
