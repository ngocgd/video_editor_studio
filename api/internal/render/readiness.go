package render

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
)

// renderParallelism is the render queue's default concurrency: segment
// steps run this many at a time.
const renderParallelism = 2

// Readiness is what the render page shows before a render starts.
type Readiness struct {
	// Settings are the stored settings; the encoder may still be auto.
	Settings Settings
	// Scenes are the episode's scenes with their selected takes.
	Scenes []dbgen.ListManifestSceneInputsRow
	// Reasons lists why a render cannot start; empty when it can.
	Reasons []string
	// Forecast is set only when a render can start.
	Forecast *Forecast
}

// Forecast is a rough forecast of a render started now.
type Forecast struct {
	DurationMs int64
	Scenes     int
	Segments   int
	// Cached counts the cache entries a render would reuse.
	Cached  int
	Encode  time.Duration
	Encoder string
}

// Check runs everything Freeze checks without writing anything. An
// unknown episode is an error (pgx.ErrNoRows); an episode that cannot
// render yet is not: its reasons are in the result.
func (f *Freezer) Check(ctx context.Context, tenantID, episodeID uuid.UUID, lang string) (Readiness, error) {
	p, err := f.prepare(ctx, tenantID, episodeID, lang)
	r := Readiness{Settings: p.stored, Scenes: p.rows}
	var notReady *NotReadyError
	if errors.As(err, &notReady) {
		r.Reasons = notReady.Reasons
		return r, nil
	}
	if err != nil {
		return r, err
	}
	r.Forecast = forecast(p)
	return r, nil
}

// forecast sums the per-kind estimates of the steps a freeze would
// enqueue, with segments sharing the render queue's parallelism.
func forecast(p prepared) *Forecast {
	cached := make(map[string]bool, len(p.cached))
	for _, h := range p.cached {
		cached[h] = true
	}
	seen := make(map[string]bool, len(p.planned))
	var work time.Duration
	for _, s := range p.planned {
		if cached[s.Input.Hash] || seen[s.Input.Hash] {
			continue
		}
		seen[s.Input.Hash] = true
		work += Estimate(s.Kind)
	}
	return &Forecast{
		DurationMs: p.timeline.DurationMs(),
		Scenes:     len(p.manifest.Scenes),
		Segments:   len(p.timeline.Segments),
		Cached:     len(cached),
		Encode:     work/renderParallelism + Estimate(KindCompose) + Estimate(KindPreview),
		Encoder:    p.manifest.Settings.Encoder,
	}
}
