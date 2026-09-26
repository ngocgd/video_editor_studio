package scenes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// Errors the HTTP layer maps to problem responses.
var (
	ErrNotFound        = errors.New("scenes: not found")
	ErrVersionConflict = errors.New("scenes: version conflict")
	ErrNoVoiceTake     = errors.New("scenes: no voice take to align")
	ErrInvalid         = errors.New("scenes: invalid request")
	ErrEmptyDraft      = errors.New("scenes: no draft text to split")
)

// Service holds the scene operations shared by the HTTP handlers and the
// step handlers (a step records its take through the same code path).
type Service struct {
	Pool    *pgxpool.Pool
	Queries *dbgen.Queries
	Engine  *pipeline.Engine
	Hooks   *Hooks
}

// RolledScene is one scene with its derived pips.
type RolledScene struct {
	Row       dbgen.SceneRollupRow
	Segments  []Segment
	Pips      map[string]Pip
	Worst     string
	Takes     map[string]*TakeInfo
	StartMs   int
	Generated []string // states of image, voice, align
}

// Rollup is an episode language's storyboard state.
type Rollup struct {
	Scenes  []RolledScene
	Inputs  EpisodeInputs
	TotalMs int
}

// LoadRollup runs the rollup query once and derives every pip.
func (s *Service) LoadRollup(ctx context.Context, tenantID uuid.UUID, episode dbgen.Episode, lang string) (Rollup, error) {
	inputs, _, err := LoadEpisodeInputs(ctx, s.Queries, tenantID, idconv.FromPg(episode.SeriesID), lang)
	if err != nil {
		return Rollup{}, err
	}
	rows, err := s.Queries.SceneRollup(ctx, dbgen.SceneRollupParams{TenantID: idconv.ToPg(tenantID), EpisodeID: episode.ID, Lang: lang})
	if err != nil {
		return Rollup{}, err
	}
	out := Rollup{Inputs: inputs, Scenes: make([]RolledScene, 0, len(rows))}
	start := 0
	for _, r := range rows {
		rs := RolledScene{Row: r, Segments: DecodeSegments(r.Segments), Pips: map[string]Pip{}, Takes: map[string]*TakeInfo{}, StartMs: start}
		start += int(r.DurationMs)
		rs.Takes[TakeImage] = decodeTake(r.ImageTake)
		rs.Takes[TakeVoice] = decodeTake(r.VoiceTake)
		rs.Takes[TakeAlign] = decodeTake(r.AlignTake)
		var voiceTake *uuid.UUID
		if t := rs.Takes[TakeVoice]; t != nil {
			voiceTake = &t.ID
		}
		in := SceneInputs{
			ID: idconv.FromPg(r.ID), Narration: r.Narration, Segments: rs.Segments, ImagePrompt: r.ImagePrompt,
			CharacterIDs: fromPgIDs(r.CharacterIds), ImageStyleID: idconv.FromPgPtr(r.ImageStyleID), VoiceTakeID: voiceTake,
		}
		steps := map[string]*StepInfo{TakeImage: decodeStep(r.ImageStep), TakeVoice: decodeStep(r.VoiceStep), TakeAlign: decodeStep(r.AlignStep)}
		for _, kind := range []string{TakeImage, TakeVoice, TakeAlign} {
			p := PipFor(kind, steps[kind], rs.Takes[kind], ComponentsFor(kind, inputs, in))
			rs.Pips[kind] = p
			rs.Generated = append(rs.Generated, p.State)
		}
		textState := StateDone
		if strings.TrimSpace(r.Narration) == "" {
			textState = StateNone
		}
		rs.Pips["text"] = Pip{Kind: "text", State: textState}
		rs.Pips["motion"] = Pip{Kind: "motion", State: StateDone}
		rs.Worst = WorstState(append([]string{textState}, rs.Generated...)...)
		out.Scenes = append(out.Scenes, rs)
	}
	out.TotalMs = start
	return out, nil
}

func decodeStep(raw []byte) *StepInfo {
	if len(raw) == 0 {
		return nil
	}
	var s StepInfo
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	return &s
}

func decodeTake(raw []byte) *TakeInfo {
	if len(raw) == 0 {
		return nil
	}
	var t TakeInfo
	if json.Unmarshal(raw, &t) != nil {
		return nil
	}
	return &t
}

// SplitResult is what an applied split changed.
type SplitResult struct {
	Total, Kept, Unrecognised int
}

// ApplySplit replaces an episode language's scenes with drafts in one
// transaction. A scene whose narration hash matches an existing scene
// keeps that row (id, edits and every take) and only moves position;
// all other old scenes are deleted with their takes.
func (s *Service) ApplySplit(ctx context.Context, tenantID, episodeID uuid.UUID, lang string, drafts []Draft, styleID *uuid.UUID) (SplitResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return SplitResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)
	tid, eid := idconv.ToPg(tenantID), idconv.ToPg(episodeID)

	existing, err := q.ListScenes(ctx, dbgen.ListScenesParams{TenantID: tid, EpisodeID: eid, Lang: lang})
	if err != nil {
		return SplitResult{}, err
	}
	hashes := make([]string, len(existing))
	for i, sc := range existing {
		hashes[i] = sc.TextHash
	}
	plan := PlanResplit(hashes, drafts)

	res := SplitResult{Total: len(drafts)}
	keep := make([]uuid.UUID, 0, len(drafts))
	for i, d := range drafts {
		hash := TextHash(d.Narration)
		if k := plan[i]; k >= 0 {
			old := existing[k]
			if _, err := q.ResplitKeepScene(ctx, dbgen.ResplitKeepSceneParams{
				Idx: int32(i + 1), ParagraphIds: d.ParagraphIDs, Tainted: d.Tainted, TenantID: tid, ID: old.ID,
			}); err != nil {
				return SplitResult{}, err
			}
			keep = append(keep, idconv.FromPg(old.ID))
			res.Kept++
			continue
		}
		segs, err := json.Marshal(nonNilSegments(d.Segments))
		if err != nil {
			return SplitResult{}, err
		}
		id := idconv.NewV7()
		if _, err := q.InsertScene(ctx, dbgen.InsertSceneParams{
			ID: idconv.ToPg(id), TenantID: tid, EpisodeID: eid, Lang: lang, Idx: int32(i + 1), ParagraphIds: d.ParagraphIDs,
			Narration: d.Narration, Segments: segs, ImagePrompt: d.ImagePrompt, CharacterIds: pgIDs(d.CharacterIDs),
			ImageStyleID: idconv.ToPgPtr(styleID), DurationMs: int32(d.EstimatedMs), TextHash: hash, Tainted: d.Tainted,
		}); err != nil {
			return SplitResult{}, err
		}
		keep = append(keep, id)
		for _, seg := range d.Segments {
			if seg.UnrecognisedName != "" {
				res.Unrecognised++
			}
		}
	}
	if _, err := q.DeleteScenesExcept(ctx, dbgen.DeleteScenesExceptParams{TenantID: tid, EpisodeID: eid, Lang: lang, KeepIds: pgIDs(keep)}); err != nil {
		return SplitResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SplitResult{}, err
	}
	s.Hooks.Emit(ctx, Changed{TenantID: tenantID, EpisodeID: episodeID, Lang: lang, SceneIDs: keep, Reason: "split"})
	return res, nil
}

// PlanResplit decides which existing scene (by position in existing)
// each new draft keeps: the first not-yet-kept scene with the same
// narration hash, or -1 for a new scene. Duplicate narrations pair up in
// order.
func PlanResplit(existing []string, drafts []Draft) []int {
	byHash := map[string][]int{}
	for i, h := range existing {
		byHash[h] = append(byHash[h], i)
	}
	out := make([]int, len(drafts))
	for i, d := range drafts {
		h := TextHash(d.Narration)
		if olds := byHash[h]; len(olds) > 0 {
			out[i] = olds[0]
			byHash[h] = olds[1:]
		} else {
			out[i] = -1
		}
	}
	return out
}

func nonNilSegments(s []Segment) []Segment {
	if s == nil {
		return []Segment{}
	}
	return s
}

// Patch is a scene edit; nil fields are unchanged.
type Patch struct {
	ExpectedVersion int64
	Narration       *string
	Segments        []Segment
	ImagePrompt     *string
	CharacterIDs    *[]uuid.UUID
	MotionPreset    *string
	ImageStyleID    *uuid.UUID
	ClearImageStyle bool
}

// UpdateScene applies an edit under optimistic concurrency and emits
// exactly one Changed event.
func (s *Service) UpdateScene(ctx context.Context, tenantID, sceneID uuid.UUID, p Patch) (dbgen.Scene, error) {
	tid := idconv.ToPg(tenantID)
	scene, err := s.Queries.GetScene(ctx, dbgen.GetSceneParams{TenantID: tid, ID: idconv.ToPg(sceneID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Scene{}, ErrNotFound
	}
	if err != nil {
		return dbgen.Scene{}, err
	}
	episode, err := s.Queries.GetEpisodeByID(ctx, dbgen.GetEpisodeByIDParams{TenantID: tid, ID: scene.EpisodeID})
	if err != nil {
		return dbgen.Scene{}, err
	}
	allowed, err := s.seriesCharacterSet(ctx, tenantID, idconv.FromPg(episode.SeriesID))
	if err != nil {
		return dbgen.Scene{}, err
	}

	narration := scene.Narration
	segments := DecodeSegments(scene.Segments)
	switch {
	case p.Segments != nil:
		for _, seg := range p.Segments {
			if seg.SpeakerCharacterID != nil && !allowed[*seg.SpeakerCharacterID] {
				return dbgen.Scene{}, fmt.Errorf("%w: a segment speaker is not a character of this series", ErrInvalid)
			}
		}
		segments = p.Segments
		narration = SegmentsText(segments)
		if p.Narration != nil {
			narration = *p.Narration
		}
	case p.Narration != nil:
		narration = *p.Narration
		segments = ResegmentKeepingSpeakers(segments, narration)
	}
	prompt := scene.ImagePrompt
	if p.ImagePrompt != nil {
		prompt = *p.ImagePrompt
	}
	charIDs := fromPgIDs(scene.CharacterIds)
	if p.CharacterIDs != nil {
		for _, id := range *p.CharacterIDs {
			if !allowed[id] {
				return dbgen.Scene{}, fmt.Errorf("%w: a character is not in this series", ErrInvalid)
			}
		}
		charIDs = *p.CharacterIDs
	}
	motion := scene.MotionPreset
	if p.MotionPreset != nil {
		motion = *p.MotionPreset
	}
	styleID := scene.ImageStyleID
	switch {
	case p.ClearImageStyle:
		styleID = idconv.ToPgPtr(nil)
	case p.ImageStyleID != nil:
		if _, err := s.Queries.GetImageStyle(ctx, dbgen.GetImageStyleParams{TenantID: tid, ID: idconv.ToPg(*p.ImageStyleID)}); err != nil {
			return dbgen.Scene{}, fmt.Errorf("%w: unknown image style", ErrInvalid)
		}
		styleID = idconv.ToPg(*p.ImageStyleID)
	}
	segJSON, err := json.Marshal(nonNilSegments(segments))
	if err != nil {
		return dbgen.Scene{}, err
	}
	updated, err := s.Queries.UpdateSceneEdit(ctx, dbgen.UpdateSceneEditParams{
		Narration: narration, Segments: segJSON, ImagePrompt: prompt, CharacterIds: pgIDs(charIDs), MotionPreset: motion,
		ImageStyleID: styleID, TextHash: TextHash(narration), EstimatedDurationMs: int32(EstimateMs(narration, scene.Lang)),
		TenantID: tid, ID: scene.ID, ExpectedVersion: p.ExpectedVersion,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Scene{}, ErrVersionConflict
	}
	if err != nil {
		return dbgen.Scene{}, err
	}
	s.Hooks.Emit(ctx, Changed{TenantID: tenantID, EpisodeID: idconv.FromPg(scene.EpisodeID), Lang: scene.Lang, SceneIDs: []uuid.UUID{sceneID}, Reason: "edit"})
	return updated, nil
}

func (s *Service) seriesCharacterSet(ctx context.Context, tenantID, seriesID uuid.UUID) (map[uuid.UUID]bool, error) {
	chars, err := s.Queries.ListCharactersBySeries(ctx, dbgen.ListCharactersBySeriesParams{TenantID: idconv.ToPg(tenantID), SeriesID: idconv.ToPg(seriesID)})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(chars))
	for _, c := range chars {
		out[idconv.FromPg(c.ID)] = true
	}
	return out, nil
}

// ResegmentKeepingSpeakers re-derives segments from edited narration:
// each quoted line keeps the speaker of the quoted line at the same
// position before the edit; new ones go to the narrator.
func ResegmentKeepingSpeakers(old []Segment, narration string) []Segment {
	// A segment can hold several quoted lines (consecutive lines of one
	// speaker, or the narrator's own lines merged with narration), so the
	// old speakers are recovered per quoted span, not per segment.
	var quoteSpeakers []Segment
	for _, s := range old {
		for _, span := range SplitSpans(s.Text) {
			if span.Quote {
				quoteSpeakers = append(quoteSpeakers, s)
			}
		}
	}
	var out []Segment
	n := 0
	for _, span := range SplitSpans(narration) {
		seg := Segment{Text: span.Text}
		if span.Quote {
			if n < len(quoteSpeakers) {
				seg.SpeakerCharacterID = quoteSpeakers[n].SpeakerCharacterID
				seg.UnrecognisedName = quoteSpeakers[n].UnrecognisedName
			}
			n++
		}
		out = appendSegment(out, seg)
	}
	return out
}

// SelectTake makes takeID the scene's selected take of its kind. A voice
// take also sets the scene's measured duration.
func (s *Service) SelectTake(ctx context.Context, tenantID, sceneID, takeID uuid.UUID) (dbgen.Scene, error) {
	tid := idconv.ToPg(tenantID)
	take, err := s.Queries.GetTake(ctx, dbgen.GetTakeParams{TenantID: tid, ID: idconv.ToPg(takeID)})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && idconv.FromPg(take.SceneID) != sceneID) {
		return dbgen.Scene{}, ErrNotFound
	}
	if err != nil {
		return dbgen.Scene{}, err
	}
	scene, err := s.selectTake(ctx, tenantID, take)
	if err != nil {
		return dbgen.Scene{}, err
	}
	s.Hooks.Emit(ctx, Changed{TenantID: tenantID, EpisodeID: idconv.FromPg(scene.EpisodeID), Lang: scene.Lang, SceneIDs: []uuid.UUID{sceneID}, Reason: "take"})
	return scene, nil
}

func (s *Service) selectTake(ctx context.Context, tenantID uuid.UUID, take dbgen.SceneTake) (dbgen.Scene, error) {
	tid := idconv.ToPg(tenantID)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return dbgen.Scene{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)
	if err := q.UnselectTakes(ctx, dbgen.UnselectTakesParams{TenantID: tid, SceneID: take.SceneID, Kind: take.Kind}); err != nil {
		return dbgen.Scene{}, err
	}
	if _, err := q.MarkTakeSelected(ctx, dbgen.MarkTakeSelectedParams{TenantID: tid, ID: take.ID}); err != nil {
		return dbgen.Scene{}, err
	}
	var scene dbgen.Scene
	if take.Kind == TakeVoice {
		asset, err := q.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: tid, ID: take.AssetID})
		if err != nil {
			return dbgen.Scene{}, err
		}
		if asset.DurationMs.Valid {
			scene, err = q.SetSceneMeasuredDuration(ctx, dbgen.SetSceneMeasuredDurationParams{DurationMs: asset.DurationMs.Int32, TenantID: tid, ID: take.SceneID})
			if err != nil {
				return dbgen.Scene{}, err
			}
		}
	}
	if !scene.ID.Valid {
		if scene, err = q.TouchScene(ctx, dbgen.TouchSceneParams{TenantID: tid, ID: take.SceneID}); err != nil {
			return dbgen.Scene{}, err
		}
	}
	return scene, tx.Commit(ctx)
}

// RecordTake stores a step's output as a new take, selects it, and
// announces the change (hook + SSE scene.updated on the step's run).
func (s *Service) RecordTake(ctx context.Context, tenantID, sceneID uuid.UUID, kind string, assetID uuid.UUID, components Components, extra map[string]any, stepID, runID uuid.UUID) (dbgen.SceneTake, error) {
	params, err := json.Marshal(TakeParams{Components: components, Extra: extra})
	if err != nil {
		return dbgen.SceneTake{}, err
	}
	take, err := s.Queries.InsertTake(ctx, dbgen.InsertTakeParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(tenantID), SceneID: idconv.ToPg(sceneID), Kind: kind,
		AssetID: idconv.ToPg(assetID), Params: params, InputHash: components.Hash(), StepID: idconv.ToPg(stepID),
	})
	if err != nil {
		return dbgen.SceneTake{}, err
	}
	scene, err := s.selectTake(ctx, tenantID, take)
	if err != nil {
		return dbgen.SceneTake{}, err
	}
	s.Hooks.Emit(ctx, Changed{TenantID: tenantID, EpisodeID: idconv.FromPg(scene.EpisodeID), Lang: scene.Lang, SceneIDs: []uuid.UUID{sceneID}, Reason: "take"})
	notifySceneUpdated(ctx, s.Pool, tenantID, runID, sceneID, scene.Version)
	return take, nil
}

// sceneEvent is an SSE payload shaped like a step event, carried on the
// run topic of the step that changed the scene, so a storyboard already
// subscribed to that run refetches it. Ids and numbers only.
type sceneEvent struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	RunID      string `json:"run_id"`
	TenantID   string `json:"tenant_id"`
	Status     string `json:"status"`
	Version    int64  `json:"version"`
	Transition bool   `json:"transition"`
}

func notifySceneUpdated(ctx context.Context, pool *pgxpool.Pool, tenantID, runID, sceneID uuid.UUID, version int64) {
	payload, err := json.Marshal(sceneEvent{Type: "scene.updated", ID: sceneID.String(), RunID: runID.String(), TenantID: tenantID.String(), Status: "done", Version: version, Transition: true})
	if err != nil {
		return
	}
	if _, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", pipeline.NotifyChannel, string(payload)); err != nil {
		slog.WarnContext(ctx, "scenes: failed to publish scene.updated", "error", err)
	}
}

// Regenerate queues exactly one step for one kind of one scene at the
// per-scene priority.
func (s *Service) Regenerate(ctx context.Context, tenantID uuid.UUID, createdBy *uuid.UUID, sceneID uuid.UUID, kind string) (uuid.UUID, uuid.UUID, error) {
	tid := idconv.ToPg(tenantID)
	scene, err := s.Queries.GetScene(ctx, dbgen.GetSceneParams{TenantID: tid, ID: idconv.ToPg(sceneID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if kind == TakeAlign {
		if _, err := s.Queries.GetSelectedTake(ctx, dbgen.GetSelectedTakeParams{TenantID: tid, SceneID: scene.ID, Kind: TakeVoice}); err != nil {
			return uuid.Nil, uuid.Nil, ErrNoVoiceTake
		}
	}
	stepID := idconv.NewV7()
	runID, err := s.Engine.Enqueue(ctx, tenantID, pipeline.RunSpec{
		ID: idconv.NewV7(), ScopeKind: ScopeScene, ScopeID: sceneID, Kind: "scene.regenerate", CreatedBy: createdBy,
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: StepKindFor[kind], ScopeKind: ScopeScene, ScopeID: sceneID, Priority: pipeline.PriorityScene}},
	})
	return runID, stepID, err
}

// MissingCounts is how many steps of each kind a batch queued.
type MissingCounts struct{ Image, Voice, Align int }

// GenerateMissing queues, as one batch run at batch priority, every
// image, voice and align step whose pip is missing, stale or failed. An
// align step for a scene whose voice is also queued depends on it. The
// engine groups the steps by (queue, kind, model) and chunks them, so
// the GPU switches model at most once per stage.
func (s *Service) GenerateMissing(ctx context.Context, tenantID uuid.UUID, createdBy *uuid.UUID, episode dbgen.Episode, lang string, kinds map[string]bool) (uuid.UUID, MissingCounts, error) {
	roll, err := s.LoadRollup(ctx, tenantID, episode, lang)
	if err != nil {
		return uuid.Nil, MissingCounts{}, err
	}
	steps, counts := PlanMissing(roll, kinds)
	if len(steps) == 0 {
		return uuid.Nil, counts, nil
	}
	runID, err := s.Engine.Enqueue(ctx, tenantID, pipeline.RunSpec{
		ID: idconv.NewV7(), ScopeKind: ScopeEpisode, ScopeID: idconv.FromPg(episode.ID), Kind: "scenes.generate_missing", CreatedBy: createdBy, Steps: steps,
	})
	return runID, counts, err
}

// PlanMissing builds the batch steps for a rollup.
func PlanMissing(roll Rollup, kinds map[string]bool) ([]pipeline.StepSpec, MissingCounts) {
	want := func(k string) bool { return len(kinds) == 0 || kinds[k] }
	var steps []pipeline.StepSpec
	var counts MissingCounts
	for _, sc := range roll.Scenes {
		sceneID := idconv.FromPg(sc.Row.ID)
		if want(TakeImage) && NeedsGeneration(sc.Pips[TakeImage].State) {
			steps = append(steps, pipeline.StepSpec{ID: idconv.NewV7(), Kind: KindImage, ScopeKind: ScopeScene, ScopeID: sceneID, Priority: pipeline.PriorityBatch})
			counts.Image++
		}
		var voiceStep *uuid.UUID
		if want(TakeVoice) && NeedsGeneration(sc.Pips[TakeVoice].State) {
			id := idconv.NewV7()
			voiceStep = &id
			steps = append(steps, pipeline.StepSpec{ID: id, Kind: KindVoice, ScopeKind: ScopeScene, ScopeID: sceneID, Priority: pipeline.PriorityBatch})
			counts.Voice++
		}
		alignState := sc.Pips[TakeAlign].State
		voiceReady := sc.Takes[TakeVoice] != nil
		if want(TakeAlign) && alignState != StateQueued && alignState != StateRunning && (voiceStep != nil || (voiceReady && NeedsGeneration(alignState))) {
			spec := pipeline.StepSpec{ID: idconv.NewV7(), Kind: KindAlign, ScopeKind: ScopeScene, ScopeID: sceneID, Priority: pipeline.PriorityBatch}
			if voiceStep != nil {
				spec.DependsOn = []uuid.UUID{*voiceStep}
			}
			steps = append(steps, spec)
			counts.Align++
		}
	}
	return steps, counts
}

// MissingCount is the "Generate missing (N)" figure.
func MissingCount(roll Rollup) int {
	_, c := PlanMissing(roll, nil)
	return c.Image + c.Voice + c.Align
}
