package render

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/storage"
)

// Step kinds of the render DAG. Every one runs on the render queue and
// reads only its frozen manifest.
const (
	KindSceneBody   = "render.scene_body"
	KindTransition  = "render.transition"
	KindAudioMaster = "render.audio_master"
	KindSubtitles   = "render.subtitles"
	KindCompose     = "render.compose"
	KindPreview     = "render.preview"
	// ScopeManifest is the scope of every render step and run: scope_id
	// is the manifest.
	ScopeManifest = "render_manifest"
	// RunKind is the pipeline run kind of a render.
	RunKind = "render"
)

// FontsDir is where the worker image installs the subtitle fonts libass
// burns with.
const FontsDir = "/usr/share/fonts/loomtale"

// Deps is what the render steps need. In the api process only Queries
// is set: it enqueues render runs but never runs a step, and Run
// refuses without storage and ffmpeg.
type Deps struct {
	Queries *dbgen.Queries
	Storage *storage.Internal
	Runner  *ffmpeg.Runner
	Prober  *ffmpeg.Prober
	Encoder *EncoderProbe
	// FontsDir overrides FontsDir (tests on a host without the image).
	FontsDir string
}

func (d Deps) fontsDir() string {
	if d.FontsDir != "" {
		return d.FontsDir
	}
	return FontsDir
}

func (d Deps) ready() error {
	if d.Storage == nil || d.Runner == nil || d.Prober == nil {
		return fmt.Errorf("%w: ffmpeg or storage is not configured in this process", pipeline.ErrEngineNotInstalled)
	}
	return nil
}

// StepInput is the enqueue-time input of every render step. Hash is the
// step's content hash, computed at freeze time from the manifest only;
// Segment indexes Timeline.Segments for body and transition steps.
type StepInput struct {
	ManifestID string `json:"manifestId"`
	Segment    int    `json:"segment,omitempty"`
	Hash       string `json:"hash"`
}

// Handlers returns every render step handler.
func Handlers(d Deps) []pipeline.StepHandler {
	return []pipeline.StepHandler{
		&segmentHandler{Deps: d, kind: KindSceneBody}, &segmentHandler{Deps: d, kind: KindTransition},
		&AudioHandler{Deps: d}, &SubtitlesHandler{Deps: d}, &ComposeHandler{Deps: d}, &PreviewHandler{Deps: d},
	}
}

// Estimate is the per-kind duration estimate batches are chunked by.
func Estimate(kind string) time.Duration {
	switch kind {
	case KindSceneBody, KindTransition:
		return 20 * time.Second
	case KindAudioMaster, KindSubtitles:
		return 30 * time.Second
	case KindCompose, KindPreview:
		return 2 * time.Minute
	default:
		return pipeline.DefaultStepEstimate(kind)
	}
}

// stepBase is the part every render handler shares: the render queue,
// no GPU model, and the input hash written at freeze time.
type stepBase struct{}

func (stepBase) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueRender, nil
}

func (stepBase) InputHash(_ context.Context, s pipeline.StepRef) (string, error) {
	var in StepInput
	if err := json.Unmarshal(s.Input, &in); err != nil || in.Hash == "" {
		return "", fmt.Errorf("%w: a render step needs its manifest hash", pipeline.ErrValidation)
	}
	return in.Hash, nil
}

func (stepBase) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return nil, nil
}

// job is a loaded manifest: what every render step works from.
type job struct {
	Row      dbgen.RenderManifest
	Manifest Manifest
	Timeline Timeline
	Tenant   uuid.UUID
	Episode  uuid.UUID
	Lang     string
}

// loadJob reads the manifest a step belongs to.
func loadJob(ctx context.Context, q *dbgen.Queries, tenantID uuid.UUID, manifestID uuid.UUID) (*job, error) {
	row, err := q.GetRenderManifest(ctx, dbgen.GetRenderManifestParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(manifestID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: render manifest %s does not exist", pipeline.ErrValidation, manifestID)
	}
	if err != nil {
		return nil, err
	}
	j := &job{Row: row, Tenant: tenantID, Episode: idconv.FromPg(row.EpisodeID), Lang: row.Lang}
	if err := json.Unmarshal(row.Settings, &j.Manifest.Settings); err != nil {
		return nil, fmt.Errorf("%w: manifest settings: %v", pipeline.ErrValidation, err)
	}
	if err := json.Unmarshal(row.Scenes, &j.Manifest.Scenes); err != nil {
		return nil, fmt.Errorf("%w: manifest scenes: %v", pipeline.ErrValidation, err)
	}
	if j.Manifest.Hash() != row.Hash {
		return nil, fmt.Errorf("%w: render manifest %s does not match its hash", pipeline.ErrValidation, manifestID)
	}
	if j.Timeline, err = j.Manifest.Timeline(); err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	return j, nil
}

// stepJob reads a running step's input and manifest.
func stepJob(ctx context.Context, d Deps, sc *pipeline.StepContext) (*job, StepInput, error) {
	var in StepInput
	if err := sc.Input(&in); err != nil {
		return nil, in, fmt.Errorf("%w: render step input: %v", pipeline.ErrValidation, err)
	}
	id, err := uuid.Parse(in.ManifestID)
	if err != nil || sc.ScopeID() != id {
		return nil, in, fmt.Errorf("%w: render step input names another manifest", pipeline.ErrValidation)
	}
	j, err := loadJob(ctx, d.Queries, sc.Tenant(), id)
	return j, in, err
}

// segmentKey is the workers-only storage key of a cached render object.
func (j *job) segmentKey(hash, ext string) string {
	return storage.RenderKey(j.Tenant.String(), j.Episode.String()+"/"+j.Lang+"/seg/"+hash+ext)
}
