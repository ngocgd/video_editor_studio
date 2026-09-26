package render

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// EncoderSource reports the encoder the render worker's probe found, as
// the worker publishes it. ok is false while no worker has reported.
type EncoderSource interface {
	RenderEncoder(ctx context.Context) (info EncoderInfo, ok bool)
}

// NotReadyError lists why an episode cannot be rendered yet. Callers
// show the reasons next to the disabled render button.
type NotReadyError struct{ Reasons []string }

func (e *NotReadyError) Error() string {
	return "render: episode is not ready: " + strings.Join(e.Reasons, " ")
}

// Is makes a NotReadyError a validation error for pipeline and HTTP
// error mapping.
func (e *NotReadyError) Is(target error) bool { return target == pipeline.ErrValidation }

// Freezer freezes render manifests and enqueues their DAG.
type Freezer struct {
	Pool    *pgxpool.Pool
	Queries *dbgen.Queries
	Engine  *pipeline.Engine
	// Encoder resolves the "auto" encoder; nil (or no report) means
	// libx264, which every worker has.
	Encoder EncoderSource
	// Depth reports whether the GPU has a depth model (parallax); nil
	// means it has none.
	Depth func(ctx context.Context) bool
}

// FreezeRequest names the episode language to render.
type FreezeRequest struct {
	TenantID  uuid.UUID
	EpisodeID uuid.UUID
	Lang      string
	CreatedBy *uuid.UUID
	// AfterEdit marks a manifest frozen because a scene edit superseded
	// the running render.
	AfterEdit bool
}

// Frozen is a new manifest and the run rendering it.
type Frozen struct {
	ManifestID uuid.UUID
	RunID      uuid.UUID
	Hash       string
	// Steps is how many cacheable steps were enqueued; Reused how many
	// cache entries already existed and needed no step.
	Steps  int
	Reused int
}

// Settings returns the episode language's stored settings, or the
// defaults when none were saved.
func (f *Freezer) Settings(ctx context.Context, q *dbgen.Queries, tenantID, episodeID uuid.UUID, lang string) (Settings, error) {
	row, err := q.GetRenderSettings(ctx, dbgen.GetRenderSettingsParams{TenantID: idconv.ToPg(tenantID), EpisodeID: idconv.ToPg(episodeID), Lang: lang})
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return SettingsFromRow(row)
}

// resolveEncoder turns the settings' choice into the concrete encoder
// the manifest freezes.
func (f *Freezer) resolveEncoder(ctx context.Context, choice string) (string, error) {
	var info EncoderInfo
	if f.Encoder != nil {
		info, _ = f.Encoder.RenderEncoder(ctx)
	}
	return ResolveEncoder(choice, info)
}

// Freeze snapshots the episode language's settings and selected takes
// into a manifest, pins the cache entries it needs, and enqueues a run
// with a step for every entry not cached yet plus compose and preview.
func (f *Freezer) Freeze(ctx context.Context, req FreezeRequest) (Frozen, error) {
	q := f.Queries
	p, err := f.prepare(ctx, req.TenantID, req.EpisodeID, req.Lang)
	if err != nil {
		return Frozen{}, err
	}
	manifestID, runID := idconv.NewV7(), idconv.NewV7()
	planned := planSteps(p.manifest, p.timeline, manifestID.String(), req.Lang)
	specs, reused, err := stepSpecs(planned, p.cached, manifestID, p.manifest.Hash())
	if err != nil {
		return Frozen{}, err
	}
	if err := f.insertManifest(ctx, req, manifestID, p.manifest, p.pins, reused); err != nil {
		return Frozen{}, err
	}
	if _, err := f.Engine.Enqueue(ctx, req.TenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: ScopeManifest, ScopeID: manifestID, Kind: RunKind, CreatedBy: req.CreatedBy, Steps: specs,
	}); err != nil {
		_ = q.DeleteRenderManifest(context.WithoutCancel(ctx), dbgen.DeleteRenderManifestParams{TenantID: idconv.ToPg(req.TenantID), ID: idconv.ToPg(manifestID)})
		return Frozen{}, err
	}
	if _, err := q.SetManifestRun(ctx, dbgen.SetManifestRunParams{RunID: idconv.ToPg(runID), TenantID: idconv.ToPg(req.TenantID), ID: idconv.ToPg(manifestID)}); err != nil {
		return Frozen{}, err
	}
	return Frozen{ManifestID: manifestID, RunID: runID, Hash: p.manifest.Hash(), Steps: len(specs) - 2, Reused: reused}, nil
}

// prepared is everything a freeze computes before it writes anything.
// On a NotReadyError the fields reached so far are still set, so the
// render page can show progress next to the reasons.
type prepared struct {
	stored   Settings
	rows     []dbgen.ListManifestSceneInputsRow
	manifest Manifest
	timeline Timeline
	// planned carries no manifest id: hashes never depend on it.
	planned []plannedStep
	pins    []string
	cached  []string
}

// prepare reads the settings and selected takes, resolves the encoder,
// and plans the timeline and its cache entries, without writing.
func (f *Freezer) prepare(ctx context.Context, tenantID, episodeID uuid.UUID, lang string) (prepared, error) {
	q := f.Queries
	var p prepared
	ep, err := q.GetEpisodeForRender(ctx, dbgen.GetEpisodeForRenderParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(episodeID)})
	if err != nil {
		return p, err
	}
	if p.stored, err = f.Settings(ctx, q, tenantID, episodeID, lang); err != nil {
		return p, err
	}
	if p.rows, err = q.ListManifestSceneInputs(ctx, dbgen.ListManifestSceneInputsParams{TenantID: idconv.ToPg(tenantID), EpisodeID: idconv.ToPg(episodeID), Lang: lang}); err != nil {
		return p, err
	}
	if !slices.Contains(ep.TargetLanguages, lang) {
		return p, &NotReadyError{Reasons: []string{fmt.Sprintf("The series does not target language %q.", lang)}}
	}
	settings := p.stored
	if settings.Encoder, err = f.resolveEncoder(ctx, settings.Encoder); err != nil {
		return p, &NotReadyError{Reasons: []string{"The render worker has no NVENC encoder; choose auto or libx264."}}
	}
	depth := f.Depth != nil && f.Depth(ctx)
	scenes, reasons := ManifestScenes(p.rows, settings.FPS, depth)
	if len(reasons) > 0 {
		return p, &NotReadyError{Reasons: reasons}
	}
	p.manifest = Manifest{Settings: settings, Scenes: scenes}
	if p.timeline, err = p.manifest.Timeline(); err != nil {
		return p, &NotReadyError{Reasons: []string{err.Error()}}
	}
	p.planned = planSteps(p.manifest, p.timeline, "", lang)
	p.pins = pinnedHashes(p.planned)
	if p.cached, err = q.ListCachedSegmentHashes(ctx, dbgen.ListCachedSegmentHashesParams{TenantID: idconv.ToPg(tenantID), InputHashes: p.pins}); err != nil {
		return p, err
	}
	return p, nil
}

// insertManifest writes the manifest and its pinned cache entries in one
// transaction, so TTL cleanup never sees one without the other.
func (f *Freezer) insertManifest(ctx context.Context, req FreezeRequest, id uuid.UUID, m Manifest, pins []string, reused int) error {
	settingsJSON, err := json.Marshal(m.Settings)
	if err != nil {
		return err
	}
	scenesJSON, err := json.Marshal(m.Scenes)
	if err != nil {
		return err
	}
	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := f.Queries.WithTx(tx)
	if _, err := qtx.InsertRenderManifest(ctx, dbgen.InsertRenderManifestParams{
		ID: idconv.ToPg(id), TenantID: idconv.ToPg(req.TenantID), EpisodeID: idconv.ToPg(req.EpisodeID), Lang: req.Lang,
		Settings: settingsJSON, SettingsHash: m.SettingsHash(), Scenes: scenesJSON, Hash: m.Hash(), CreatedBy: idconv.ToPgPtr(req.CreatedBy),
		RestartedAfterEdit: req.AfterEdit, ReusedSegments: int32(reused), //nolint:gosec // bounded by the scene count
	}); err != nil {
		return err
	}
	if err := qtx.InsertManifestSegments(ctx, dbgen.InsertManifestSegmentsParams{ManifestID: idconv.ToPg(id), TenantID: idconv.ToPg(req.TenantID), InputHashes: pins}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// stepSpecs builds the run's steps: one per planned step whose hash is
// neither cached nor already planned, then compose depending on all of
// them and preview depending on compose. reused counts the planned
// steps a cache entry made unnecessary.
func stepSpecs(planned []plannedStep, cachedHashes []string, manifestID uuid.UUID, manifestHash string) ([]pipeline.StepSpec, int, error) {
	cached := make(map[string]bool, len(cachedHashes))
	for _, h := range cachedHashes {
		cached[h] = true
	}
	seen := make(map[string]bool, len(planned))
	var specs []pipeline.StepSpec
	var deps []uuid.UUID
	reused := 0
	for _, p := range planned {
		if cached[p.Input.Hash] {
			reused++
			continue
		}
		if seen[p.Input.Hash] {
			continue
		}
		seen[p.Input.Hash] = true
		spec, err := newSpec(p.Kind, manifestID, p.Input, nil)
		if err != nil {
			return nil, 0, err
		}
		specs = append(specs, spec)
		deps = append(deps, spec.ID)
	}
	compose, err := newSpec(KindCompose, manifestID, StepInput{ManifestID: manifestID.String(), Hash: manifestHash}, deps)
	if err != nil {
		return nil, 0, err
	}
	preview, err := newSpec(KindPreview, manifestID, StepInput{ManifestID: manifestID.String(), Hash: PreviewHash(manifestHash)}, []uuid.UUID{compose.ID})
	if err != nil {
		return nil, 0, err
	}
	return append(specs, compose, preview), reused, nil
}

func newSpec(kind string, manifestID uuid.UUID, in StepInput, deps []uuid.UUID) (pipeline.StepSpec, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return pipeline.StepSpec{}, err
	}
	return pipeline.StepSpec{
		ID: idconv.NewV7(), Kind: kind, ScopeKind: ScopeManifest, ScopeID: manifestID,
		Priority: pipeline.PriorityBatch, DependsOn: deps, Input: raw,
	}, nil
}
