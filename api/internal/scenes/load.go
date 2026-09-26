package scenes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// Storyboard defaults when a series has no settings row.
const defaultGapMs = 150

// Settings is a series' storyboard defaults.
type Settings struct {
	ImageStyleID *uuid.UUID
	Cadence      Cadence
	GapMs        int
}

// LoadSettings reads a series' storyboard settings, or the defaults.
func LoadSettings(ctx context.Context, q *dbgen.Queries, tenantID, seriesID uuid.UUID) (Settings, error) {
	row, err := q.GetStoryboardSettings(ctx, dbgen.GetStoryboardSettingsParams{TenantID: idconv.ToPg(tenantID), SeriesID: idconv.ToPg(seriesID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{Cadence: DefaultCadence, GapMs: defaultGapMs}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		ImageStyleID: idconv.FromPgPtr(row.ImageStyleID),
		Cadence:      Cadence{MinS: int(row.CadenceMinS), MaxS: int(row.CadenceMaxS)},
		GapMs:        int(row.SegmentGapMs),
	}, nil
}

// StyleFromRow converts an image_styles row.
func StyleFromRow(r dbgen.ImageStyle) Style {
	var loras []Lora
	_ = json.Unmarshal(r.Loras, &loras)
	return Style{
		ID: idconv.FromPg(r.ID), StylePrompt: r.StylePrompt, NegativePrompt: r.NegativePrompt, BaseModel: r.BaseModel,
		Sampler: r.Sampler, Steps: int(r.Steps), Width: int(r.Width), Height: int(r.Height), Loras: loras,
	}
}

// DisplayName is a character's name in lang, falling back through the
// other languages.
func DisplayName(c dbgen.Character, lang string) string {
	order := []string{c.NameEn, c.NameOrig, c.NameVi}
	if lang == "vi" {
		order = []string{c.NameVi, c.NameEn, c.NameOrig}
	}
	for _, n := range order {
		if n != "" {
			return n
		}
	}
	return ""
}

// LoadEpisodeInputs loads everything an episode's scene hashes depend on
// in a fixed handful of queries.
func LoadEpisodeInputs(ctx context.Context, q *dbgen.Queries, tenantID uuid.UUID, seriesID uuid.UUID, lang string) (EpisodeInputs, []dbgen.Character, error) {
	tid := idconv.ToPg(tenantID)
	settings, err := LoadSettings(ctx, q, tenantID, seriesID)
	if err != nil {
		return EpisodeInputs{}, nil, err
	}
	in := EpisodeInputs{Lang: lang, Styles: map[uuid.UUID]Style{}, Characters: map[uuid.UUID]CharacterLook{}, Voices: map[uuid.UUID]Voice{}, GapMs: settings.GapMs}

	styles, err := q.ListImageStyles(ctx, tid)
	if err != nil {
		return in, nil, err
	}
	for _, s := range styles {
		in.Styles[idconv.FromPg(s.ID)] = StyleFromRow(s)
	}
	if settings.ImageStyleID != nil {
		if st, ok := in.Styles[*settings.ImageStyleID]; ok {
			in.DefaultStyle = &st
		}
	}
	if in.DefaultStyle == nil && len(styles) > 0 {
		first, err := q.FirstImageStyle(ctx, tid)
		if err == nil {
			st := StyleFromRow(first)
			in.DefaultStyle = &st
		}
	}

	chars, err := q.ListCharactersBySeries(ctx, dbgen.ListCharactersBySeriesParams{TenantID: tid, SeriesID: idconv.ToPg(seriesID)})
	if err != nil {
		return in, nil, err
	}
	ids := make([]pgtype.UUID, len(chars))
	for i, c := range chars {
		ids[i] = c.ID
		in.Characters[idconv.FromPg(c.ID)] = CharacterLook{
			ID: idconv.FromPg(c.ID), Name: DisplayName(c, lang), AppearancePrompt: c.AppearancePrompt,
			NegativePrompt: c.NegativePrompt, TriggerToken: c.TriggerToken,
		}
	}
	if len(ids) > 0 {
		loras, err := q.LatestReadyLoras(ctx, dbgen.LatestReadyLorasParams{TenantID: tid, CharacterIds: ids})
		if err != nil {
			return in, nil, err
		}
		for _, l := range loras {
			id := idconv.FromPg(l.CharacterID)
			look := in.Characters[id]
			look.LoraVersion, look.LoraFile = int(l.Version), l.WeightsFile
			in.Characters[id] = look
		}
	}

	voices, err := q.ListCharacterVoicesBySeries(ctx, dbgen.ListCharacterVoicesBySeriesParams{TenantID: tid, SeriesID: idconv.ToPg(seriesID)})
	if err != nil {
		return in, nil, err
	}
	narrators, err := q.ListNarratorVoicesBySeries(ctx, dbgen.ListNarratorVoicesBySeriesParams{TenantID: tid, SeriesID: idconv.ToPg(seriesID)})
	if err != nil {
		return in, nil, err
	}
	presets, err := loadPresets(ctx, q, tid, voices, narrators)
	if err != nil {
		return in, nil, err
	}
	for _, v := range voices {
		if v.Lang == lang {
			in.Voices[idconv.FromPg(v.CharacterID)] = voiceFrom(v.Engine, v.VoicePresetID, v.Params, presets)
		}
	}
	for _, n := range narrators {
		if n.Lang == lang {
			v := voiceFrom(n.Engine, n.VoicePresetID, n.Params, presets)
			in.Narrator = &v
		}
	}
	return in, chars, nil
}

func loadPresets(ctx context.Context, q *dbgen.Queries, tid pgtype.UUID, voices []dbgen.CharacterVoice, narrators []dbgen.NarratorVoice) (map[uuid.UUID]dbgen.VoicePreset, error) {
	var ids []pgtype.UUID
	for _, v := range voices {
		if v.VoicePresetID.Valid {
			ids = append(ids, v.VoicePresetID)
		}
	}
	for _, n := range narrators {
		if n.VoicePresetID.Valid {
			ids = append(ids, n.VoicePresetID)
		}
	}
	out := map[uuid.UUID]dbgen.VoicePreset{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.GetVoicePresetsByIDs(ctx, dbgen.GetVoicePresetsByIDsParams{TenantID: tid, Ids: ids})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[idconv.FromPg(r.ID)] = r
	}
	return out, nil
}

func voiceFrom(engine string, presetID pgtype.UUID, params []byte, presets map[uuid.UUID]dbgen.VoicePreset) Voice {
	v := Voice{Engine: engine, Params: decodeStringMap(params)}
	if id := idconv.FromPgPtr(presetID); id != nil {
		v.PresetID = id
		if p, ok := presets[*id]; ok {
			v.PresetParams = decodeStringMap(p.Params)
			v.RefAssetID = idconv.FromPgPtr(p.RefAudioAssetID)
			v.Consented = p.ConsentedAt.Valid
		}
	}
	return v
}

func decodeStringMap(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// DecodeSegments reads scenes.segments.
func DecodeSegments(raw []byte) []Segment {
	var segs []Segment
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &segs)
	}
	return segs
}

func pgIDs(ids []uuid.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = idconv.ToPg(id)
	}
	return out
}

func fromPgIDs(ids []pgtype.UUID) []uuid.UUID {
	out := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		out[i] = idconv.FromPg(id)
	}
	return out
}

// SceneInputsOf converts a scene row (plus its selected voice take id).
func SceneInputsOf(s dbgen.Scene, voiceTake *uuid.UUID) SceneInputs {
	return SceneInputs{
		ID: idconv.FromPg(s.ID), Narration: s.Narration, Segments: DecodeSegments(s.Segments), ImagePrompt: s.ImagePrompt,
		CharacterIDs: fromPgIDs(s.CharacterIds), ImageStyleID: idconv.FromPgPtr(s.ImageStyleID), VoiceTakeID: voiceTake,
	}
}

// sceneContext is one scene with its episode and resolved inputs, as a
// step handler needs it.
type sceneContext struct {
	Scene      dbgen.Scene
	Episode    dbgen.Episode
	Inputs     EpisodeInputs
	Characters []dbgen.Character
	VoiceTake  *dbgen.SceneTake
}

func (sc sceneContext) sceneInputs() SceneInputs {
	var take *uuid.UUID
	if sc.VoiceTake != nil {
		id := idconv.FromPg(sc.VoiceTake.ID)
		take = &id
	}
	return SceneInputsOf(sc.Scene, take)
}

// inputsCacheKey marks a context that memoizes LoadEpisodeInputs, so a
// batch enqueue (hundreds of steps whose InputHash and ModelRef each need
// the same episode inputs) loads them once instead of once per call.
type inputsCacheKey struct{}

type inputsCache struct {
	mu      sync.Mutex
	entries map[string]cachedInputs
}

type cachedInputs struct {
	inputs EpisodeInputs
	chars  []dbgen.Character
}

// WithInputsCache returns ctx carrying a fresh episode-inputs memo, for
// the span of one batch enqueue.
func WithInputsCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, inputsCacheKey{}, &inputsCache{entries: map[string]cachedInputs{}})
}

func loadEpisodeInputsCached(ctx context.Context, q *dbgen.Queries, tenantID, seriesID uuid.UUID, lang string) (EpisodeInputs, []dbgen.Character, error) {
	cache, _ := ctx.Value(inputsCacheKey{}).(*inputsCache)
	if cache == nil {
		return LoadEpisodeInputs(ctx, q, tenantID, seriesID, lang)
	}
	key := tenantID.String() + "/" + seriesID.String() + "/" + lang
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if hit, ok := cache.entries[key]; ok {
		return hit.inputs, hit.chars, nil
	}
	inputs, chars, err := LoadEpisodeInputs(ctx, q, tenantID, seriesID, lang)
	if err == nil {
		cache.entries[key] = cachedInputs{inputs: inputs, chars: chars}
	}
	return inputs, chars, err
}

// loadSceneContext loads a scene of tenantID with everything its hashes
// and steps need.
func loadSceneContext(ctx context.Context, q *dbgen.Queries, tenantID, sceneID uuid.UUID) (sceneContext, error) {
	tid := idconv.ToPg(tenantID)
	scene, err := q.GetScene(ctx, dbgen.GetSceneParams{TenantID: tid, ID: idconv.ToPg(sceneID)})
	if err != nil {
		return sceneContext{}, fmt.Errorf("scenes: load scene: %w", err)
	}
	episode, err := q.GetEpisodeByID(ctx, dbgen.GetEpisodeByIDParams{TenantID: tid, ID: scene.EpisodeID})
	if err != nil {
		return sceneContext{}, fmt.Errorf("scenes: load episode: %w", err)
	}
	inputs, chars, err := loadEpisodeInputsCached(ctx, q, tenantID, idconv.FromPg(episode.SeriesID), scene.Lang)
	if err != nil {
		return sceneContext{}, err
	}
	out := sceneContext{Scene: scene, Episode: episode, Inputs: inputs, Characters: chars}
	take, err := q.GetSelectedTake(ctx, dbgen.GetSelectedTakeParams{TenantID: tid, SceneID: scene.ID, Kind: TakeVoice})
	switch {
	case err == nil:
		out.VoiceTake = &take
	case !errors.Is(err, pgx.ErrNoRows):
		return sceneContext{}, err
	}
	return out, nil
}

func idOf(id pgtype.UUID) uuid.UUID { return idconv.FromPg(id) }
