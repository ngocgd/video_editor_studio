package scenes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/media"
	"loomtale/api/internal/storage"
	"loomtale/api/internal/tenant"
)

// ScenesAPI implements the storyboard slice of gen.StrictServerInterface.
type ScenesAPI struct {
	Service *Service
	Storage *storage.Internal
}

func problem(status int, title string) gen.Problem {
	return gen.Problem{Title: title, Status: status}
}

func creator(ctx context.Context) *uuid.UUID {
	if sess, ok := authpkg.FromCtx(ctx); ok && sess.UserID != uuid.Nil {
		id := sess.UserID
		return &id
	}
	return nil
}

func (h *ScenesAPI) episode(ctx context.Context, tenantID, id uuid.UUID) (dbgen.Episode, error) {
	return h.Service.Queries.GetEpisodeByID(ctx, dbgen.GetEpisodeByIDParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(id)})
}

// GetStoryboardSettings implements gen.StrictServerInterface.
func (h *ScenesAPI) GetStoryboardSettings(ctx context.Context, req gen.GetStoryboardSettingsRequestObject) (gen.GetStoryboardSettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, err := h.Service.Queries.GetSeriesByID(ctx, dbgen.GetSeriesByIDParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)}); err != nil {
		return gen.GetStoryboardSettings404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "series not found")), nil
	}
	s, err := LoadSettings(ctx, h.Service.Queries, info.ID, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.GetStoryboardSettings200JSONResponse{ImageStyleId: s.ImageStyleID, CadenceMinS: s.Cadence.MinS, CadenceMaxS: s.Cadence.MaxS, SegmentGapMs: s.GapMs}, nil
}

// PutStoryboardSettings implements gen.StrictServerInterface.
func (h *ScenesAPI) PutStoryboardSettings(ctx context.Context, req gen.PutStoryboardSettingsRequestObject) (gen.PutStoryboardSettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	if _, err := h.Service.Queries.GetSeriesByID(ctx, dbgen.GetSeriesByIDParams{TenantID: tid, ID: idconv.ToPg(req.Id)}); err != nil {
		return gen.PutStoryboardSettings404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "series not found")), nil
	}
	if req.Body.CadenceMinS > req.Body.CadenceMaxS {
		return gen.PutStoryboardSettings400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "cadenceMinS must not exceed cadenceMaxS")), nil
	}
	if req.Body.ImageStyleId != nil {
		if _, err := h.Service.Queries.GetImageStyle(ctx, dbgen.GetImageStyleParams{TenantID: tid, ID: idconv.ToPg(*req.Body.ImageStyleId)}); err != nil {
			return gen.PutStoryboardSettings404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "image style not found")), nil
		}
	}
	row, err := h.Service.Queries.UpsertStoryboardSettings(ctx, dbgen.UpsertStoryboardSettingsParams{
		SeriesID: idconv.ToPg(req.Id), TenantID: tid, ImageStyleID: idconv.ToPgPtr(req.Body.ImageStyleId),
		CadenceMinS: int32(req.Body.CadenceMinS), CadenceMaxS: int32(req.Body.CadenceMaxS), SegmentGapMs: int32(req.Body.SegmentGapMs),
	})
	if err != nil {
		return nil, err
	}
	return gen.PutStoryboardSettings200JSONResponse{ImageStyleId: idconv.FromPgPtr(row.ImageStyleID), CadenceMinS: int(row.CadenceMinS), CadenceMaxS: int(row.CadenceMaxS), SegmentGapMs: int(row.SegmentGapMs)}, nil
}

// ListScenes implements gen.StrictServerInterface: one rollup query,
// then filtering, search, counts and paging in memory (an episode has at
// most a few hundred scenes).
func (h *ScenesAPI) ListScenes(ctx context.Context, req gen.ListScenesRequestObject) (gen.ListScenesResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	episode, err := h.episode(ctx, info.ID, req.Id)
	if err != nil {
		return gen.ListScenes404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "episode not found")), nil
	}
	roll, err := h.Service.LoadRollup(ctx, info.ID, episode, string(req.Params.Lang))
	if err != nil {
		return nil, err
	}
	filter := FilterAll
	if req.Params.Filter != nil {
		filter = string(*req.Params.Filter)
	}
	q := ""
	if req.Params.Q != nil {
		q = *req.Params.Q
	}
	cursor := 0
	if req.Params.Cursor != nil {
		cursor, _ = strconv.Atoi(*req.Params.Cursor)
	}
	limit := 500
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}

	resp := gen.ListScenes200JSONResponse{Items: []gen.Scene{}, ActiveRunIds: []uuid.UUID{}, TotalDurationMs: roll.TotalMs, MissingCount: MissingCount(roll)}
	stageDone := map[string]int{}
	active := map[uuid.UUID]bool{}
	for _, rs := range roll.Scenes {
		resp.Counts.All++
		if Matches(FilterStale, rs.Generated) {
			resp.Counts.Stale++
		}
		if Matches(FilterFailed, rs.Generated) {
			resp.Counts.Failed++
		}
		if Matches(FilterMissing, rs.Generated) {
			resp.Counts.Missing++
		}
		if Matches(FilterInQueue, rs.Generated) {
			resp.Counts.InQueue++
		}
		for kind, pip := range rs.Pips {
			if pip.State == StateDone {
				stageDone[kind]++
			}
			if pip.Step != nil && (pip.State == StateQueued || pip.State == StateRunning) {
				active[pip.Step.RunID] = true
			}
		}
		if int(rs.Row.Idx) <= cursor || !Matches(filter, rs.Generated) || !MatchesQuery(q, rs.Row.Narration) {
			continue
		}
		if len(resp.Items) == limit {
			next := strconv.Itoa(int(resp.Items[len(resp.Items)-1].Idx))
			resp.NextCursor = &next
			continue
		}
		resp.Items = append(resp.Items, SceneDTO(rs))
	}
	for _, kind := range []string{"text", TakeImage, TakeVoice, TakeAlign, "motion"} {
		resp.Stages = append(resp.Stages, gen.StageProgress{Kind: gen.PipKind(kind), Done: stageDone[kind], Total: len(roll.Scenes)})
	}
	for id := range active {
		resp.ActiveRunIds = append(resp.ActiveRunIds, id)
	}
	return resp, nil
}

// SceneDTO converts a rolled-up scene.
func SceneDTO(rs RolledScene) gen.Scene {
	r := rs.Row
	dto := gen.Scene{
		Id: idOf(r.ID), EpisodeId: idOf(r.EpisodeID), Lang: gen.SceneLanguage(r.Lang), Idx: int(r.Idx),
		ParagraphIds: nonNilStrings(r.ParagraphIds), Narration: r.Narration, ImagePrompt: r.ImagePrompt,
		CharacterIds: fromPgIDs(r.CharacterIds), MotionPreset: gen.MotionPreset(r.MotionPreset), ImageStyleId: idconv.FromPgPtr(r.ImageStyleID),
		DurationMs: int(r.DurationMs), DurationMeasured: r.DurationMeasured, StartMs: rs.StartMs, Tainted: r.Tainted, Version: r.Version,
		WorstState: gen.PipState(rs.Worst), Segments: make([]gen.SceneSegment, 0, len(rs.Segments)),
	}
	for _, s := range rs.Segments {
		seg := gen.SceneSegment{SpeakerCharacterId: s.SpeakerCharacterID, Text: s.Text}
		if s.UnrecognisedName != "" {
			name := s.UnrecognisedName
			seg.UnrecognisedName = &name
		}
		dto.Segments = append(dto.Segments, seg)
	}
	for _, kind := range []string{"text", TakeImage, TakeVoice, TakeAlign, "motion"} {
		p := rs.Pips[kind]
		pip := gen.ScenePip{Kind: gen.PipKind(kind), State: gen.PipState(p.State)}
		if p.StaleReason != "" {
			reason := p.StaleReason
			pip.StaleReason = &reason
		}
		if p.Step != nil {
			stepID, runID, progress := p.Step.ID, p.Step.RunID, p.Step.Progress
			pip.StepId, pip.RunId, pip.Progress = &stepID, &runID, &progress
			if p.State == StateFailed {
				code, msg := p.Step.ErrorCode, p.Step.ErrorMsg
				pip.ErrorCode, pip.ErrorMessage = &code, &msg
			}
		}
		dto.Pips = append(dto.Pips, pip)
	}
	if t := rs.Takes[TakeImage]; t != nil {
		id := t.AssetID
		dto.ImageAssetId = &id
		has := t.Variants["webp"] != nil
		dto.ImageVariants = &has
	}
	if t := rs.Takes[TakeVoice]; t != nil {
		id := t.AssetID
		dto.VoiceAssetId = &id
		dto.Peaks = t.Variants["peaks"] != nil
	}
	if t := rs.Takes[TakeAlign]; t != nil {
		id := t.AssetID
		dto.AlignAssetId = &id
	}
	return dto
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// sceneDTOByID re-runs the rollup and returns one scene of it.
func (h *ScenesAPI) sceneDTOByID(ctx context.Context, tenantID uuid.UUID, scene dbgen.Scene) (gen.Scene, error) {
	episode, err := h.Service.Queries.GetEpisodeByID(ctx, dbgen.GetEpisodeByIDParams{TenantID: idconv.ToPg(tenantID), ID: scene.EpisodeID})
	if err != nil {
		return gen.Scene{}, err
	}
	roll, err := h.Service.LoadRollup(ctx, tenantID, episode, scene.Lang)
	if err != nil {
		return gen.Scene{}, err
	}
	for _, rs := range roll.Scenes {
		if rs.Row.ID == scene.ID {
			return SceneDTO(rs), nil
		}
	}
	return gen.Scene{}, pgx.ErrNoRows
}

// SplitScenes implements gen.StrictServerInterface.
func (h *ScenesAPI) SplitScenes(ctx context.Context, req gen.SplitScenesRequestObject) (gen.SplitScenesResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	lang := string(req.Body.Lang)
	minS, maxS := deref(req.Body.CadenceMinS), deref(req.Body.CadenceMaxS)
	if minS > 0 && maxS > 0 && minS > maxS {
		return gen.SplitScenes422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "cadenceMinS must not exceed cadenceMaxS")), nil
	}
	discard := req.Body.DiscardWork != nil && *req.Body.DiscardWork
	if req.Body.Mode == gen.SceneSplitRequestModeParagraphs {
		res, err := h.Service.SplitByParagraphsNow(ctx, info.ID, req.Id, lang, minS, maxS, discard)
		if resp, done := splitError(err); done {
			return resp, nil
		}
		if err != nil {
			return nil, err
		}
		unrec, dropped := res.Unrecognised, res.Dropped
		return gen.SplitScenes200JSONResponse{
			Mode: gen.SceneSplitResultModeParagraphs, SceneCount: res.Total, KeptCount: res.Kept, DroppedCount: &dropped, UnrecognisedSpeakers: &unrec,
		}, nil
	}
	runID, stepID, err := h.Service.EnqueueLLMSplit(ctx, info.ID, creator(ctx), req.Id, SplitInput{Lang: lang, CadenceMinS: minS, CadenceMaxS: maxS, DiscardWork: discard})
	if resp, done := splitError(err); done {
		return resp, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.SplitScenes202JSONResponse{Mode: gen.SceneSplitResultModeLlm, RunId: &runID, StepId: &stepID}, nil
}

func splitError(err error) (gen.SplitScenesResponseObject, bool) {
	if drops := (*DropsWorkError)(nil); errors.As(err, &drops) {
		return gen.SplitScenes409ApplicationProblemPlusJSONResponse(gen.SceneSplitConflict{
			Title: "the split would delete edited scenes or takes", Status: http.StatusConflict, Detail: drops.Error(),
			DroppedCount: drops.Risk.Dropped, EditedCount: drops.Risk.Edited, TakeCount: drops.Risk.Takes,
		}), true
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return gen.SplitScenes404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "episode not found")), true
	case errors.Is(err, ErrEmptyDraft):
		return gen.SplitScenes422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "no draft text in this language to split")), true
	}
	return nil, false
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// GenerateMissing implements gen.StrictServerInterface.
func (h *ScenesAPI) GenerateMissing(ctx context.Context, req gen.GenerateMissingRequestObject) (gen.GenerateMissingResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	episode, err := h.episode(ctx, info.ID, req.Id)
	if err != nil {
		return gen.GenerateMissing404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "episode not found")), nil
	}
	kinds := map[string]bool{}
	if req.Body.Kinds != nil {
		for _, k := range *req.Body.Kinds {
			kinds[string(k)] = true
		}
	}
	runID, counts, err := h.Service.GenerateMissing(ctx, info.ID, creator(ctx), episode, string(req.Body.Lang), kinds)
	if err != nil {
		return nil, err
	}
	resp := gen.GenerateMissing202JSONResponse{}
	resp.Queued.Image, resp.Queued.Voice, resp.Queued.Align = counts.Image, counts.Voice, counts.Align
	if runID != uuid.Nil {
		resp.RunId = &runID
	}
	return resp, nil
}

// UpdateScene implements gen.StrictServerInterface.
func (h *ScenesAPI) UpdateScene(ctx context.Context, req gen.UpdateSceneRequestObject) (gen.UpdateSceneResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	p := Patch{ExpectedVersion: req.Body.ExpectedVersion, Narration: req.Body.Narration, ImagePrompt: req.Body.ImagePrompt, ImageStyleID: req.Body.ImageStyleId}
	if req.Body.Segments != nil {
		p.Segments = make([]Segment, 0, len(*req.Body.Segments))
		for _, s := range *req.Body.Segments {
			seg := Segment{SpeakerCharacterID: s.SpeakerCharacterId, Text: s.Text}
			if s.UnrecognisedName != nil && s.SpeakerCharacterId == nil {
				seg.UnrecognisedName = *s.UnrecognisedName
			}
			p.Segments = append(p.Segments, seg)
		}
	}
	if req.Body.CharacterIds != nil {
		ids := append([]uuid.UUID(nil), *req.Body.CharacterIds...)
		p.CharacterIDs = &ids
	}
	if req.Body.MotionPreset != nil {
		m := string(*req.Body.MotionPreset)
		p.MotionPreset = &m
	}
	p.ClearImageStyle = req.Body.ClearImageStyle != nil && *req.Body.ClearImageStyle
	scene, err := h.Service.UpdateScene(ctx, info.ID, req.Id, p)
	switch {
	case errors.Is(err, ErrNotFound):
		return gen.UpdateScene404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "scene not found")), nil
	case errors.Is(err, ErrVersionConflict):
		return gen.UpdateScene409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "the scene changed since you loaded it")), nil
	case errors.Is(err, ErrInvalid):
		detail := err.Error()
		return gen.UpdateScene400ApplicationProblemPlusJSONResponse{Title: "invalid scene edit", Status: http.StatusBadRequest, Detail: &detail}, nil
	case err != nil:
		return nil, err
	}
	dto, err := h.sceneDTOByID(ctx, info.ID, scene)
	if err != nil {
		return nil, err
	}
	return gen.UpdateScene200JSONResponse(dto), nil
}

// RegenerateScene implements gen.StrictServerInterface.
func (h *ScenesAPI) RegenerateScene(ctx context.Context, req gen.RegenerateSceneRequestObject) (gen.RegenerateSceneResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	runID, stepID, err := h.Service.Regenerate(ctx, info.ID, creator(ctx), req.Id, string(req.Body.Kind))
	switch {
	case errors.Is(err, ErrNotFound):
		return gen.RegenerateScene404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "scene not found")), nil
	case errors.Is(err, ErrNoVoiceTake):
		return gen.RegenerateScene422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "the scene has no voice take to align yet")), nil
	case err != nil:
		return nil, err
	}
	return gen.RegenerateScene202JSONResponse{RunId: runID, StepIds: []uuid.UUID{stepID}}, nil
}

// ListSceneTakes implements gen.StrictServerInterface.
func (h *ScenesAPI) ListSceneTakes(ctx context.Context, req gen.ListSceneTakesRequestObject) (gen.ListSceneTakesResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	scene, err := h.Service.Queries.GetScene(ctx, dbgen.GetSceneParams{TenantID: tid, ID: idconv.ToPg(req.Id)})
	if err != nil {
		return gen.ListSceneTakes404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "scene not found")), nil
	}
	scx, err := loadSceneContext(ctx, h.Service.Queries, info.ID, req.Id)
	if err != nil {
		return nil, err
	}
	rows, err := h.Service.Queries.ListTakes(ctx, dbgen.ListTakesParams{TenantID: tid, SceneID: scene.ID})
	if err != nil {
		return nil, err
	}
	in := scx.sceneInputs()
	current := map[string]string{}
	for _, k := range []string{TakeImage, TakeVoice, TakeAlign} {
		current[k] = ComponentsFor(k, scx.Inputs, in).Hash()
	}
	out := gen.ListSceneTakes200JSONResponse{Items: make([]gen.SceneTake, 0, len(rows))}
	for _, r := range rows {
		var params map[string]any
		_ = json.Unmarshal(r.Params, &params)
		variants := media.DecodeVariants(r.Variants)
		t := gen.SceneTake{
			Id: idOf(r.ID), Kind: gen.TakeKind(r.Kind), AssetId: idOf(r.AssetID), Selected: r.Selected, Stale: r.InputHash != current[r.Kind],
			CreatedAt: r.CreatedAt.Time.UTC().Truncate(time.Millisecond), Params: params, Variants: len(variants.WebP) > 0 || variants.Peaks != "",
		}
		if r.AssetDurationMs.Valid {
			d := int(r.AssetDurationMs.Int32)
			t.DurationMs = &d
		}
		out.Items = append(out.Items, t)
	}
	return out, nil
}

// SelectSceneTake implements gen.StrictServerInterface.
func (h *ScenesAPI) SelectSceneTake(ctx context.Context, req gen.SelectSceneTakeRequestObject) (gen.SelectSceneTakeResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	scene, err := h.Service.SelectTake(ctx, info.ID, req.Id, req.TakeId)
	if errors.Is(err, ErrNotFound) {
		return gen.SelectSceneTake404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "scene or take not found")), nil
	}
	if err != nil {
		return nil, err
	}
	dto, err := h.sceneDTOByID(ctx, info.ID, scene)
	if err != nil {
		return nil, err
	}
	return gen.SelectSceneTake200JSONResponse(dto), nil
}

// GetScenePeaks implements gen.StrictServerInterface: the waveform of the
// scene's selected voice take, sliced to the requested window so the
// timeline only fetches what it draws.
func (h *ScenesAPI) GetScenePeaks(ctx context.Context, req gen.GetScenePeaksRequestObject) (gen.GetScenePeaksResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	take, err := h.Service.Queries.GetSelectedTake(ctx, dbgen.GetSelectedTakeParams{TenantID: tid, SceneID: idconv.ToPg(req.Id), Kind: TakeVoice})
	if err != nil {
		return gen.GetScenePeaks404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "scene has no voice take")), nil
	}
	asset, err := h.Service.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: tid, ID: take.AssetID})
	if err != nil {
		return nil, err
	}
	key := media.DecodeVariants(asset.Variants).Peaks
	if key == "" {
		return gen.GetScenePeaks404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "peaks not computed yet")), nil
	}
	peaks, err := media.ReadPeaks(ctx, h.Storage, key)
	if err != nil {
		return nil, err
	}
	start, window := peaks.Window(int64(deref(req.Params.StartMs)), int64(deref(req.Params.EndMs)))
	resp := gen.GetScenePeaks200JSONResponse{PeaksPerSecond: window.PeaksPerSecond, StartMs: int(start), DurationMs: int(window.DurationMs), Min: make([]int, len(window.Min)), Max: make([]int, len(window.Max))}
	for i := range window.Min {
		resp.Min[i], resp.Max[i] = int(window.Min[i]), int(window.Max[i])
	}
	return resp, nil
}
