// Package characters implements a series' characters: names per
// language, prompts, the profile pinned into LLM requests, reference
// images, LoRA versions and voices, plus the character sheet, LoRA
// training and voice preview steps.
package characters

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/media"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/storyctx"
	"loomtale/api/internal/tenant"
)

// CharactersAPI implements the characters slice of gen.StrictServerInterface.
type CharactersAPI struct {
	Queries *dbgen.Queries
	Engine  *pipeline.Engine
}

func problem(status int, title string) gen.Problem { return gen.Problem{Title: title, Status: status} }

func creator(ctx context.Context) *uuid.UUID {
	if sess, ok := authpkg.FromCtx(ctx); ok && sess.UserID != uuid.Nil {
		id := sess.UserID
		return &id
	}
	return nil
}

func decodeParams(raw []byte) gen.StringParams {
	m := gen.StringParams{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func encodeParams(p *gen.StringParams) []byte {
	m := map[string]string{}
	if p != nil {
		m = *p
	}
	b, _ := json.Marshal(m)
	return b
}

// seriesData is everything the list DTO needs, in five queries.
type seriesData struct {
	chars       []dbgen.Character
	refs        map[uuid.UUID][]gen.CharacterRef
	loras       map[uuid.UUID][]gen.CharacterLora
	voices      map[uuid.UUID][]gen.CharacterVoice
	appearances map[uuid.UUID][]gen.CharacterAppearance
}

func (h *CharactersAPI) loadSeries(ctx context.Context, tid, sid pgtype.UUID) (seriesData, error) {
	d := seriesData{refs: map[uuid.UUID][]gen.CharacterRef{}, loras: map[uuid.UUID][]gen.CharacterLora{}, voices: map[uuid.UUID][]gen.CharacterVoice{}, appearances: map[uuid.UUID][]gen.CharacterAppearance{}}
	var err error
	if d.chars, err = h.Queries.ListCharactersBySeries(ctx, dbgen.ListCharactersBySeriesParams{TenantID: tid, SeriesID: sid}); err != nil {
		return d, err
	}
	refs, err := h.Queries.ListCharacterRefsBySeries(ctx, dbgen.ListCharacterRefsBySeriesParams{TenantID: tid, SeriesID: sid})
	if err != nil {
		return d, err
	}
	for _, r := range refs {
		id := idconv.FromPg(r.CharacterID)
		d.refs[id] = append(d.refs[id], refDTO(r))
	}
	loras, err := h.Queries.ListCharacterLorasBySeries(ctx, dbgen.ListCharacterLorasBySeriesParams{TenantID: tid, SeriesID: sid})
	if err != nil {
		return d, err
	}
	for _, l := range loras {
		id := idconv.FromPg(l.CharacterID)
		d.loras[id] = append(d.loras[id], loraDTO(l))
	}
	voices, err := h.Queries.ListCharacterVoicesBySeries(ctx, dbgen.ListCharacterVoicesBySeriesParams{TenantID: tid, SeriesID: sid})
	if err != nil {
		return d, err
	}
	for _, v := range voices {
		id := idconv.FromPg(v.CharacterID)
		d.voices[id] = append(d.voices[id], gen.CharacterVoice{Lang: gen.VoiceLanguage(v.Lang), Engine: v.Engine, VoicePresetId: idconv.FromPgPtr(v.VoicePresetID), Params: decodeParams(v.Params)})
	}
	apps, err := h.Queries.CharacterEpisodeAppearances(ctx, dbgen.CharacterEpisodeAppearancesParams{TenantID: tid, SeriesID: sid})
	if err != nil {
		return d, err
	}
	for _, a := range apps {
		id := idconv.FromPg(a.CharacterID)
		d.appearances[id] = append(d.appearances[id], gen.CharacterAppearance{EpisodeId: idconv.FromPg(a.EpisodeID), EpisodeIdx: int(a.EpisodeIdx), SceneCount: int(a.SceneCount)})
	}
	return d, nil
}

func refDTO(r dbgen.CharacterRef) gen.CharacterRef {
	return gen.CharacterRef{Id: idconv.FromPg(r.ID), AssetId: idconv.FromPg(r.AssetID), Angle: r.Angle, Approved: r.Approved, Origin: gen.CharacterRefOrigin(r.Origin)}
}

func loraDTO(l dbgen.CharacterLora) gen.CharacterLora {
	dto := gen.CharacterLora{Id: idconv.FromPg(l.ID), Version: int(l.Version), Status: gen.CharacterLoraStatus(l.Status), DatasetSize: len(l.DatasetAssetIds),
		TrainerParams: decodeParams(l.TrainerParams), StepId: idconv.FromPgPtr(l.StepID), CreatedAt: l.CreatedAt.Time.UTC()}
	if l.WeightsFile != "" {
		f := l.WeightsFile
		dto.WeightsFile = &f
	}
	return dto
}

func characterDTO(c dbgen.Character, d seriesData) gen.Character {
	id := idconv.FromPg(c.ID)
	dto := gen.Character{
		Id: id, SeriesId: idconv.FromPg(c.SeriesID), Names: gen.CharacterNames{Orig: c.NameOrig, En: c.NameEn, Vi: c.NameVi},
		Role: c.Role, AppearancePrompt: c.AppearancePrompt, NegativePrompt: c.NegativePrompt, TriggerToken: c.TriggerToken,
		Profile: c.Profile, ProfileTokens: storyctx.EstimateTokens(c.Profile), Pinned: c.Pinned,
		Refs: orEmpty(d.refs[id]), Loras: orEmpty(d.loras[id]), Voices: orEmpty(d.voices[id]), Appearances: orEmpty(d.appearances[id]),
	}
	return dto
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ListCharacters implements gen.StrictServerInterface.
func (h *CharactersAPI) ListCharacters(ctx context.Context, req gen.ListCharactersRequestObject) (gen.ListCharactersResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid, sid := idconv.ToPg(info.ID), idconv.ToPg(req.Id)
	if _, err := h.Queries.GetSeriesByID(ctx, dbgen.GetSeriesByIDParams{TenantID: tid, ID: sid}); err != nil {
		return gen.ListCharacters404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "series not found")), nil
	}
	d, err := h.loadSeries(ctx, tid, sid)
	if err != nil {
		return nil, err
	}
	out := gen.ListCharacters200JSONResponse{Items: make([]gen.Character, 0, len(d.chars)), NarratorVoices: []gen.CharacterVoice{}}
	pinned := make([]string, 0, len(d.chars))
	for _, c := range d.chars {
		out.Items = append(out.Items, characterDTO(c, d))
		if c.Pinned && c.Profile != "" {
			pinned = append(pinned, c.Profile)
		}
	}
	out.PinnedTokens = storyctx.EstimateTokens(strings.Join(pinned, "\n"))
	narrators, err := h.Queries.ListNarratorVoicesBySeries(ctx, dbgen.ListNarratorVoicesBySeriesParams{TenantID: tid, SeriesID: sid})
	if err != nil {
		return nil, err
	}
	for _, n := range narrators {
		out.NarratorVoices = append(out.NarratorVoices, gen.CharacterVoice{Lang: gen.VoiceLanguage(n.Lang), Engine: n.Engine, VoicePresetId: idconv.FromPgPtr(n.VoicePresetID), Params: decodeParams(n.Params)})
	}
	return out, nil
}

type characterFields struct {
	role, appearance, negative, trigger, profile string
	pinned                                       bool
}

func fieldsOf(b gen.CharacterInput) (characterFields, bool) {
	f := characterFields{pinned: true}
	if b.Role != nil {
		f.role = *b.Role
	}
	if b.AppearancePrompt != nil {
		f.appearance = *b.AppearancePrompt
	}
	if b.NegativePrompt != nil {
		f.negative = *b.NegativePrompt
	}
	if b.TriggerToken != nil {
		f.trigger = *b.TriggerToken
	}
	if b.Profile != nil {
		f.profile = *b.Profile
	}
	if b.Pinned != nil {
		f.pinned = *b.Pinned
	}
	named := strings.TrimSpace(b.Names.Orig) != "" || strings.TrimSpace(b.Names.En) != "" || strings.TrimSpace(b.Names.Vi) != ""
	return f, named
}

func (h *CharactersAPI) singleDTO(ctx context.Context, c dbgen.Character) (gen.Character, error) {
	d, err := h.loadSeries(ctx, c.TenantID, c.SeriesID)
	if err != nil {
		return gen.Character{}, err
	}
	return characterDTO(c, d), nil
}

// CreateCharacter implements gen.StrictServerInterface.
func (h *CharactersAPI) CreateCharacter(ctx context.Context, req gen.CreateCharacterRequestObject) (gen.CreateCharacterResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	if _, err := h.Queries.GetSeriesByID(ctx, dbgen.GetSeriesByIDParams{TenantID: tid, ID: idconv.ToPg(req.Id)}); err != nil {
		return gen.CreateCharacter404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "series not found")), nil
	}
	f, named := fieldsOf(*req.Body)
	if !named {
		return gen.CreateCharacter400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "a character needs a name in at least one language")), nil
	}
	c, err := h.Queries.CreateCharacter(ctx, dbgen.CreateCharacterParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: tid, SeriesID: idconv.ToPg(req.Id),
		NameOrig: strings.TrimSpace(req.Body.Names.Orig), NameEn: strings.TrimSpace(req.Body.Names.En), NameVi: strings.TrimSpace(req.Body.Names.Vi),
		Role: f.role, AppearancePrompt: f.appearance, NegativePrompt: f.negative, TriggerToken: f.trigger, Profile: f.profile, Pinned: f.pinned,
	})
	if err != nil {
		return nil, err
	}
	dto, err := h.singleDTO(ctx, c)
	if err != nil {
		return nil, err
	}
	return gen.CreateCharacter201JSONResponse(dto), nil
}

// UpdateCharacter implements gen.StrictServerInterface.
func (h *CharactersAPI) UpdateCharacter(ctx context.Context, req gen.UpdateCharacterRequestObject) (gen.UpdateCharacterResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	f, named := fieldsOf(*req.Body)
	if !named {
		return gen.UpdateCharacter400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "a character needs a name in at least one language")), nil
	}
	c, err := h.Queries.UpdateCharacter(ctx, dbgen.UpdateCharacterParams{
		NameOrig: strings.TrimSpace(req.Body.Names.Orig), NameEn: strings.TrimSpace(req.Body.Names.En), NameVi: strings.TrimSpace(req.Body.Names.Vi),
		Role: f.role, AppearancePrompt: f.appearance, NegativePrompt: f.negative, TriggerToken: f.trigger, Profile: f.profile, Pinned: f.pinned,
		TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.UpdateCharacter404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "character not found")), nil
	}
	if err != nil {
		return nil, err
	}
	dto, err := h.singleDTO(ctx, c)
	if err != nil {
		return nil, err
	}
	return gen.UpdateCharacter200JSONResponse(dto), nil
}

// DeleteCharacter implements gen.StrictServerInterface.
func (h *CharactersAPI) DeleteCharacter(ctx context.Context, req gen.DeleteCharacterRequestObject) (gen.DeleteCharacterResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	n, err := h.Queries.DeleteCharacter(ctx, dbgen.DeleteCharacterParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return gen.DeleteCharacter404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "character not found")), nil
	}
	return gen.DeleteCharacter204Response{}, nil
}

func (h *CharactersAPI) character(ctx context.Context, tenantID, id uuid.UUID) (dbgen.Character, error) {
	return h.Queries.GetCharacter(ctx, dbgen.GetCharacterParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(id)})
}

// AddCharacterRef implements gen.StrictServerInterface.
func (h *CharactersAPI) AddCharacterRef(ctx context.Context, req gen.AddCharacterRefRequestObject) (gen.AddCharacterRefResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	c, err := h.character(ctx, info.ID, req.Id)
	if err != nil {
		return gen.AddCharacterRef404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "character not found")), nil
	}
	asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: tid, ID: idconv.ToPg(req.Body.AssetId)})
	if err != nil {
		return gen.AddCharacterRef404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "asset not found")), nil
	}
	if asset.Kind != "image" || asset.Status != "ready" {
		return gen.AddCharacterRef422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "the reference must be a ready image upload")), nil
	}
	angle, approved := "", false
	if req.Body.Angle != nil {
		angle = *req.Body.Angle
	}
	if req.Body.Approved != nil {
		approved = *req.Body.Approved
	}
	ref, err := h.Queries.CreateCharacterRef(ctx, dbgen.CreateCharacterRefParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: tid, CharacterID: c.ID, AssetID: asset.ID, Angle: angle, Approved: approved, Origin: "upload",
	})
	if err != nil {
		return nil, err
	}
	if media.DecodeVariants(asset.Variants).WebP == nil {
		if _, _, err := media.EnqueueDerivatives(ctx, h.Engine, info.ID, []dbgen.Asset{asset}, creator(ctx)); err != nil {
			return nil, err
		}
	}
	return gen.AddCharacterRef201JSONResponse(refDTO(ref)), nil
}

func (h *CharactersAPI) refOf(ctx context.Context, tenantID, characterID, refID uuid.UUID) (dbgen.CharacterRef, bool) {
	ref, err := h.Queries.GetCharacterRef(ctx, dbgen.GetCharacterRefParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(refID)})
	if err != nil || idconv.FromPg(ref.CharacterID) != characterID {
		return dbgen.CharacterRef{}, false
	}
	return ref, true
}

// UpdateCharacterRef implements gen.StrictServerInterface.
func (h *CharactersAPI) UpdateCharacterRef(ctx context.Context, req gen.UpdateCharacterRefRequestObject) (gen.UpdateCharacterRefResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	ref, ok := h.refOf(ctx, info.ID, req.Id, req.RefId)
	if !ok {
		return gen.UpdateCharacterRef404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "reference not found")), nil
	}
	angle, approved := ref.Angle, ref.Approved
	if req.Body.Angle != nil {
		angle = *req.Body.Angle
	}
	if req.Body.Approved != nil {
		approved = *req.Body.Approved
	}
	updated, err := h.Queries.UpdateCharacterRef(ctx, dbgen.UpdateCharacterRefParams{Angle: angle, Approved: approved, TenantID: ref.TenantID, ID: ref.ID})
	if err != nil {
		return nil, err
	}
	return gen.UpdateCharacterRef200JSONResponse(refDTO(updated)), nil
}

// DeleteCharacterRef implements gen.StrictServerInterface.
func (h *CharactersAPI) DeleteCharacterRef(ctx context.Context, req gen.DeleteCharacterRefRequestObject) (gen.DeleteCharacterRefResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	ref, ok := h.refOf(ctx, info.ID, req.Id, req.RefId)
	if !ok {
		return gen.DeleteCharacterRef404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "reference not found")), nil
	}
	if _, err := h.Queries.DeleteCharacterRef(ctx, dbgen.DeleteCharacterRefParams{TenantID: ref.TenantID, ID: ref.ID}); err != nil {
		return nil, err
	}
	return gen.DeleteCharacterRef204Response{}, nil
}

func (h *CharactersAPI) enqueueOne(ctx context.Context, tenantID uuid.UUID, kind string, characterID uuid.UUID, priority int, input any) (uuid.UUID, uuid.UUID, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	stepID := idconv.NewV7()
	runID, err := h.Engine.Enqueue(ctx, tenantID, pipeline.RunSpec{
		ID: idconv.NewV7(), ScopeKind: ScopeCharacter, ScopeID: characterID, Kind: kind, CreatedBy: creator(ctx),
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: kind, ScopeKind: ScopeCharacter, ScopeID: characterID, Priority: priority, Input: raw}},
	})
	return runID, stepID, err
}

// RegenerateCharacterSheet implements gen.StrictServerInterface.
func (h *CharactersAPI) RegenerateCharacterSheet(ctx context.Context, req gen.RegenerateCharacterSheetRequestObject) (gen.RegenerateCharacterSheetResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, ok := h.refOf(ctx, info.ID, req.Id, req.Body.RefId); !ok {
		return gen.RegenerateCharacterSheet404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "character or reference not found")), nil
	}
	in := SheetInput{RefID: req.Body.RefId}
	if req.Body.Prompt != nil {
		in.Prompt = *req.Body.Prompt
	}
	runID, stepID, err := h.enqueueOne(ctx, info.ID, KindSheet, req.Id, pipeline.PriorityScene, in)
	if err != nil {
		return nil, err
	}
	return gen.RegenerateCharacterSheet202JSONResponse{RunId: runID, StepIds: []uuid.UUID{stepID}}, nil
}

// TrainCharacterLora implements gen.StrictServerInterface: a new LoRA
// version row plus its training step at training priority.
func (h *CharactersAPI) TrainCharacterLora(ctx context.Context, req gen.TrainCharacterLoraRequestObject) (gen.TrainCharacterLoraResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	c, err := h.character(ctx, info.ID, req.Id)
	if err != nil {
		return gen.TrainCharacterLora404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "character not found")), nil
	}
	var dataset []pgtype.UUID
	if req.Body.DatasetAssetIds != nil && len(*req.Body.DatasetAssetIds) > 0 {
		for _, id := range *req.Body.DatasetAssetIds {
			a, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: tid, ID: idconv.ToPg(id)})
			if err != nil || a.Kind != "image" || a.Status != "ready" {
				return gen.TrainCharacterLora422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "every dataset item must be a ready image of this workspace")), nil
			}
			dataset = append(dataset, a.ID)
		}
	} else {
		refs, err := h.Queries.ListCharacterRefsBySeries(ctx, dbgen.ListCharacterRefsBySeriesParams{TenantID: tid, SeriesID: c.SeriesID})
		if err != nil {
			return nil, err
		}
		for _, r := range refs {
			if r.CharacterID == c.ID && r.Approved {
				dataset = append(dataset, r.AssetID)
			}
		}
	}
	if len(dataset) == 0 {
		return gen.TrainCharacterLora422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "approve at least one reference image to train on")), nil
	}
	params := map[string]string{"steps": "2000", "rank": "16"}
	if req.Body.Steps != nil {
		params["steps"] = itoa(*req.Body.Steps)
	}
	if req.Body.Rank != nil {
		params["rank"] = itoa(*req.Body.Rank)
	}
	paramsJSON, _ := json.Marshal(params)
	version, err := h.Queries.NextCharacterLoraVersion(ctx, dbgen.NextCharacterLoraVersionParams{TenantID: tid, CharacterID: c.ID})
	if err != nil {
		return nil, err
	}
	loraID, stepID := idconv.NewV7(), idconv.NewV7()
	if _, err := h.Queries.CreateCharacterLora(ctx, dbgen.CreateCharacterLoraParams{
		ID: idconv.ToPg(loraID), TenantID: tid, CharacterID: c.ID, Version: version, DatasetAssetIds: dataset,
		TrainerParams: paramsJSON, Status: "queued", StepID: idconv.ToPg(stepID),
	}); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(TrainInput{LoraID: loraID})
	runID, err := h.Engine.Enqueue(ctx, info.ID, pipeline.RunSpec{
		ID: idconv.NewV7(), ScopeKind: ScopeCharacter, ScopeID: req.Id, Kind: KindTrain, CreatedBy: creator(ctx),
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: KindTrain, ScopeKind: ScopeCharacter, ScopeID: req.Id, Priority: pipeline.PriorityTrainBench, Input: raw}},
	})
	if err != nil {
		return nil, err
	}
	return gen.TrainCharacterLora202JSONResponse{RunId: runID, StepIds: []uuid.UUID{stepID}}, nil
}

func (h *CharactersAPI) checkPreset(ctx context.Context, tid pgtype.UUID, id *uuid.UUID) bool {
	if id == nil {
		return true
	}
	_, err := h.Queries.GetVoicePreset(ctx, dbgen.GetVoicePresetParams{TenantID: tid, ID: idconv.ToPg(*id)})
	return err == nil
}

// SetCharacterVoice implements gen.StrictServerInterface.
func (h *CharactersAPI) SetCharacterVoice(ctx context.Context, req gen.SetCharacterVoiceRequestObject) (gen.SetCharacterVoiceResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	c, err := h.character(ctx, info.ID, req.Id)
	if err != nil || !h.checkPreset(ctx, tid, req.Body.VoicePresetId) {
		return gen.SetCharacterVoice404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "character or voice preset not found")), nil
	}
	v, err := h.Queries.UpsertCharacterVoice(ctx, dbgen.UpsertCharacterVoiceParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: tid, CharacterID: c.ID, Lang: string(req.Lang), Engine: req.Body.Engine,
		VoicePresetID: idconv.ToPgPtr(req.Body.VoicePresetId), Params: encodeParams(req.Body.Params),
	})
	if err != nil {
		return nil, err
	}
	return gen.SetCharacterVoice200JSONResponse{Lang: gen.VoiceLanguage(v.Lang), Engine: v.Engine, VoicePresetId: idconv.FromPgPtr(v.VoicePresetID), Params: decodeParams(v.Params)}, nil
}

// SetNarratorVoice implements gen.StrictServerInterface.
func (h *CharactersAPI) SetNarratorVoice(ctx context.Context, req gen.SetNarratorVoiceRequestObject) (gen.SetNarratorVoiceResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	if _, err := h.Queries.GetSeriesByID(ctx, dbgen.GetSeriesByIDParams{TenantID: tid, ID: idconv.ToPg(req.Id)}); err != nil || !h.checkPreset(ctx, tid, req.Body.VoicePresetId) {
		return gen.SetNarratorVoice404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "series or voice preset not found")), nil
	}
	v, err := h.Queries.UpsertNarratorVoice(ctx, dbgen.UpsertNarratorVoiceParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: tid, SeriesID: idconv.ToPg(req.Id), Lang: string(req.Lang), Engine: req.Body.Engine,
		VoicePresetID: idconv.ToPgPtr(req.Body.VoicePresetId), Params: encodeParams(req.Body.Params),
	})
	if err != nil {
		return nil, err
	}
	return gen.SetNarratorVoice200JSONResponse{Lang: gen.VoiceLanguage(v.Lang), Engine: v.Engine, VoicePresetId: idconv.FromPgPtr(v.VoicePresetID), Params: decodeParams(v.Params)}, nil
}

// PreviewCharacterVoice implements gen.StrictServerInterface.
func (h *CharactersAPI) PreviewCharacterVoice(ctx context.Context, req gen.PreviewCharacterVoiceRequestObject) (gen.PreviewCharacterVoiceResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	c, err := h.character(ctx, info.ID, req.Id)
	if err != nil {
		return gen.PreviewCharacterVoice404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "character not found")), nil
	}
	voices, err := h.Queries.ListCharacterVoicesBySeries(ctx, dbgen.ListCharacterVoicesBySeriesParams{TenantID: tid, SeriesID: c.SeriesID})
	if err != nil {
		return nil, err
	}
	found := false
	for _, v := range voices {
		found = found || (v.CharacterID == c.ID && v.Lang == string(req.Lang))
	}
	if !found {
		return gen.PreviewCharacterVoice422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "assign a voice for this language first")), nil
	}
	runID, stepID, err := h.enqueueOne(ctx, info.ID, KindPreview, req.Id, pipeline.PriorityInteractive, PreviewInput{Lang: string(req.Lang), Text: req.Body.Text})
	if err != nil {
		return nil, err
	}
	return gen.PreviewCharacterVoice202JSONResponse{RunId: runID, StepIds: []uuid.UUID{stepID}}, nil
}
