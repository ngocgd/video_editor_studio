package render

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/diskguard"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// RenderAPI implements the render slice of gen.StrictServerInterface.
type RenderAPI struct {
	Freezer *Freezer
	// Disk is the watermark the enqueue admission check uses; nil shows
	// the disk as unknown.
	Disk *diskguard.Watermark
}

// defaultRenderList is how many renders a list returns by default.
const defaultRenderList = 20

func problem(status int, title string) gen.Problem {
	return gen.Problem{Title: title, Status: status}
}

func problemDetail(status int, title string, err error) gen.Problem {
	detail := err.Error()
	return gen.Problem{Title: title, Status: status, Detail: &detail}
}

func (h *RenderAPI) episodeExists(ctx context.Context, tenantID, id uuid.UUID) bool {
	_, err := h.Freezer.Queries.GetEpisodeForRender(ctx, dbgen.GetEpisodeForRenderParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(id)})
	return err == nil
}

// GetRenderSettings implements gen.StrictServerInterface.
func (h *RenderAPI) GetRenderSettings(ctx context.Context, req gen.GetRenderSettingsRequestObject) (gen.GetRenderSettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if !h.episodeExists(ctx, info.ID, req.Id) {
		return gen.GetRenderSettings404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "episode not found")), nil
	}
	s, err := h.Freezer.Settings(ctx, h.Freezer.Queries, info.ID, req.Id, string(req.Lang))
	if err != nil {
		return nil, err
	}
	return gen.GetRenderSettings200JSONResponse(settingsToAPI(s)), nil
}

// PutRenderSettings implements gen.StrictServerInterface.
func (h *RenderAPI) PutRenderSettings(ctx context.Context, req gen.PutRenderSettingsRequestObject) (gen.PutRenderSettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if !h.episodeExists(ctx, info.ID, req.Id) {
		return gen.PutRenderSettings404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "episode not found")), nil
	}
	s := settingsFromAPI(*req.Body)
	if err := s.Validate(); err != nil {
		return gen.PutRenderSettings400ApplicationProblemPlusJSONResponse(problemDetail(http.StatusBadRequest, "invalid render settings", err)), nil
	}
	style, err := json.Marshal(s.SubtitleStyle)
	if err != nil {
		return nil, err
	}
	row, err := h.Freezer.Queries.UpsertRenderSettings(ctx, dbgen.UpsertRenderSettingsParams{
		EpisodeID: idconv.ToPg(req.Id), Lang: string(req.Lang), TenantID: idconv.ToPg(info.ID),
		Width: int32(s.Width), Height: int32(s.Height), Fps: int32(s.FPS), //nolint:gosec // validated ranges
		Encoder: s.Encoder, Subtitles: s.Subtitles, SubtitleStyle: style, DefaultMotion: s.DefaultMotion,
		CrossfadeMs: int32(s.CrossfadeMs), LoudnessLufsX10: int32(s.LoudnessLUFSx10), TruePeakDbtpX10: int32(s.TruePeakDBTPx10), //nolint:gosec // validated ranges
	})
	if err != nil {
		return nil, err
	}
	stored, err := SettingsFromRow(row)
	if err != nil {
		return nil, err
	}
	return gen.PutRenderSettings200JSONResponse(settingsToAPI(stored)), nil
}

// GetRenderStatus implements gen.StrictServerInterface.
func (h *RenderAPI) GetRenderStatus(ctx context.Context, req gen.GetRenderStatusRequestObject) (gen.GetRenderStatusResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	lang := string(req.Params.Lang)
	ready, err := h.Freezer.Check(ctx, info.ID, req.Id, lang)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetRenderStatus404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "episode not found")), nil
	}
	if err != nil {
		return nil, err
	}
	disk := diskguard.APIStatus(ctx, h.Disk)
	resp := gen.GetRenderStatus200JSONResponse{
		Lang: req.Params.Lang, Settings: settingsToAPI(ready.Settings), Disk: disk,
		Reasons: append([]string{}, ready.Reasons...),
	}
	if disk.Level == diskBlocked || (disk.Level == diskUnknown && h.Disk != nil) {
		resp.Reasons = append(resp.Reasons, disk.Message)
	}

	q := h.Freezer.Queries
	tid, eid := idconv.ToPg(info.ID), idconv.ToPg(req.Id)
	active, err := q.GetActiveRenderRun(ctx, dbgen.GetActiveRenderRunParams{TenantID: tid, EpisodeID: eid, Lang: lang})
	switch {
	case err == nil:
		runID := idconv.FromPg(active.RunID)
		resp.ActiveRunId = &runID
		resp.Reasons = append(resp.Reasons, "A render of this episode is already running.")
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	progress, err := h.latestProgress(ctx, tid, eid, lang)
	if err != nil {
		return nil, err
	}
	resp.Latest = progress.latest
	resp.Stages = stages(ready.Scenes, progress, resp.ActiveRunId != nil)
	resp.Ready = len(resp.Reasons) == 0
	if ready.Forecast != nil {
		e := ready.Forecast
		resp.Estimate = &gen.RenderEstimate{
			DurationMs: e.DurationMs, Scenes: e.Scenes, Segments: e.Segments, CachedSegments: e.Cached,
			EncodeSeconds: int(math.Ceil(e.Encode.Seconds())), Encoder: e.Encoder,
		}
	}
	return resp, nil
}

// Disk levels the render button cares about.
const (
	diskBlocked gen.DiskStatusLevel = "blocked"
	diskUnknown gen.DiskStatusLevel = "unknown"
)

// manifestProgress is the latest manifest's cache and render state.
type manifestProgress struct {
	latest   *gen.RenderRestart
	cached   int
	total    int
	rendered bool
}

func (h *RenderAPI) latestProgress(ctx context.Context, tid, eid pgtype.UUID, lang string) (manifestProgress, error) {
	q := h.Freezer.Queries
	var out manifestProgress
	m, err := q.LatestRenderManifest(ctx, dbgen.LatestRenderManifestParams{TenantID: tid, EpisodeID: eid, Lang: lang})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.latest = &gen.RenderRestart{
		ManifestId: idconv.FromPg(m.ID), RunId: idconv.FromPgPtr(m.RunID), RestartedAfterEdit: m.RestartedAfterEdit,
		ReusedSegments: int(m.ReusedSegments), CreatedAt: idconv.FromPgTimestamptz(m.CreatedAt),
	}
	p, err := q.ManifestCacheProgress(ctx, dbgen.ManifestCacheProgressParams{TenantID: tid, ManifestID: m.ID})
	if err != nil {
		return out, err
	}
	out.cached, out.total = int(p.Cached), int(p.Total)
	_, err = q.GetRenderByManifest(ctx, dbgen.GetRenderByManifestParams{TenantID: tid, ManifestID: m.ID})
	switch {
	case err == nil:
		out.rendered = true
	case !errors.Is(err, pgx.ErrNoRows):
		return out, err
	}
	return out, nil
}

// stages is the render page's stage summary: the storyboard stages from
// the scenes' selected takes, compose from the latest manifest's cache
// entries and encode from its finished render.
func stages(rows []dbgen.ListManifestSceneInputsRow, p manifestProgress, running bool) []gen.RenderStage {
	var images, voices, aligns int
	for _, r := range rows {
		if r.ImageAssetID.Valid {
			images++
		}
		if r.VoiceAssetID.Valid && r.VoiceDurationMs.Valid {
			voices++
		}
		if r.AlignAssetID.Valid {
			aligns++
		}
	}
	n := len(rows)
	script := 0
	if n > 0 {
		script = 1
	}
	rendered := 0
	if p.rendered {
		rendered = 1
	}
	compose := stage("compose", "Compose", p.cached, p.total)
	encode := stage("encode", "Encode", rendered, 1)
	if p.latest == nil {
		compose.State, encode.State = "idle", "idle"
	}
	if running && !p.rendered {
		compose.State, encode.State = "running", "running"
	}
	return []gen.RenderStage{
		stage("script", "Script", script, 1),
		stage("scenes", "Scenes", n, max(n, 1)),
		stage("images", "Images", images, n),
		stage("voice", "Voice", voices, n),
		stage("subtitles", "Subtitles", aligns, n),
		compose, encode,
	}
}

func stage(key, label string, done, total int) gen.RenderStage {
	state := "partial"
	switch {
	case total == 0 || done == 0:
		state = "missing"
	case done >= total:
		state = "done"
	}
	return gen.RenderStage{Key: gen.RenderStageKey(key), Label: label, Done: done, Total: total, State: gen.RenderStageState(state)}
}

// StartRender implements gen.StrictServerInterface.
func (h *RenderAPI) StartRender(ctx context.Context, req gen.StartRenderRequestObject) (gen.StartRenderResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	lang := string(req.Body.Lang)
	if !h.episodeExists(ctx, info.ID, req.Id) {
		return gen.StartRender404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "episode not found")), nil
	}
	_, err := h.Freezer.Queries.GetActiveRenderRun(ctx, dbgen.GetActiveRenderRunParams{TenantID: idconv.ToPg(info.ID), EpisodeID: idconv.ToPg(req.Id), Lang: lang})
	switch {
	case err == nil:
		return gen.StartRender409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "a render of this episode is already running")), nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	var createdBy *uuid.UUID
	if sess, ok := authpkg.FromCtx(ctx); ok && sess.UserID != uuid.Nil {
		id := sess.UserID
		createdBy = &id
	}
	frozen, err := h.Freezer.Freeze(ctx, FreezeRequest{TenantID: info.ID, EpisodeID: req.Id, Lang: lang, CreatedBy: createdBy})
	var notReady *NotReadyError
	switch {
	case err == nil:
	case errors.As(err, &notReady):
		return gen.StartRender422ApplicationProblemPlusJSONResponse{Title: "episode is not ready to render", Status: http.StatusUnprocessableEntity, Reasons: notReady.Reasons}, nil
	case errors.Is(err, pgx.ErrNoRows):
		return gen.StartRender404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "episode not found")), nil
	case errors.Is(err, pipeline.ErrAdmissionDenied):
		return gen.StartRender507ApplicationProblemPlusJSONResponse(problemDetail(http.StatusInsufficientStorage, "admission denied", err)), nil
	case errors.Is(err, pipeline.ErrQuotaExceeded):
		return gen.StartRender429ApplicationProblemPlusJSONResponse(problemDetail(http.StatusTooManyRequests, "quota exceeded", err)), nil
	default:
		return nil, err
	}
	return gen.StartRender202JSONResponse{RunId: frozen.RunID, ManifestId: frozen.ManifestID, Hash: frozen.Hash, Steps: frozen.Steps, Reused: frozen.Reused}, nil
}

// ListRenders implements gen.StrictServerInterface.
func (h *RenderAPI) ListRenders(ctx context.Context, req gen.ListRendersRequestObject) (gen.ListRendersResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if !h.episodeExists(ctx, info.ID, req.Id) {
		return gen.ListRenders404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "episode not found")), nil
	}
	limit := defaultRenderList
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	rows, err := h.Freezer.Queries.ListRenders(ctx, dbgen.ListRendersParams{
		TenantID: idconv.ToPg(info.ID), EpisodeID: idconv.ToPg(req.Id), Lang: string(req.Params.Lang),
		MaxRows: int32(limit), //nolint:gosec // capped by the spec at 100
	})
	if err != nil {
		return nil, err
	}
	resp := gen.ListRenders200JSONResponse{Items: make([]gen.Render, 0, len(rows))}
	for _, r := range rows {
		item, err := renderToAPI(r)
		if err != nil {
			return nil, err
		}
		resp.Items = append(resp.Items, item)
	}
	return resp, nil
}

// GetRender implements gen.StrictServerInterface.
func (h *RenderAPI) GetRender(ctx context.Context, req gen.GetRenderRequestObject) (gen.GetRenderResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	row, err := h.Freezer.Queries.GetRender(ctx, dbgen.GetRenderParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetRender404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "render not found")), nil
	}
	if err != nil {
		return nil, err
	}
	item, err := renderToAPI(row)
	if err != nil {
		return nil, err
	}
	return gen.GetRender200JSONResponse(item), nil
}

func renderToAPI(r dbgen.Render) (gen.Render, error) {
	out := gen.Render{
		Id: idconv.FromPg(r.ID), EpisodeId: idconv.FromPg(r.EpisodeID), Lang: gen.SceneLanguage(r.Lang),
		ManifestId: idconv.FromPg(r.ManifestID), AssetId: idconv.FromPg(r.AssetID),
		SrtAssetId: idconv.FromPgPtr(r.SrtAssetID), PreviewAssetId: idconv.FromPgPtr(r.PreviewAssetID),
		DurationMs: int64(r.DurationMs), Encoder: r.Encoder, CreatedAt: idconv.FromPgTimestamptz(r.CreatedAt),
	}
	// The stored report is a QCReport; the API schema mirrors its JSON.
	if err := json.Unmarshal(r.Report, &out.Report); err != nil {
		return gen.Render{}, fmt.Errorf("render: stored QC report: %w", err)
	}
	out.Report.Failures = nonNil(out.Report.Failures)
	out.Report.MissingScenes = nonNil(out.Report.MissingScenes)
	out.Report.PlaceholderScenes = nonNil(out.Report.PlaceholderScenes)
	out.Report.MissingKeyframes = nonNil(out.Report.MissingKeyframes)
	out.Report.SceneScores = nonNil(out.Report.SceneScores)
	return out, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func settingsToAPI(s Settings) gen.RenderSettings {
	return gen.RenderSettings{
		Width: s.Width, Height: s.Height, Fps: gen.RenderSettingsFps(s.FPS), Encoder: s.Encoder,
		Subtitles: gen.RenderSettingsSubtitles(s.Subtitles),
		SubtitleStyle: gen.RenderSubtitleStyle{
			Font: s.SubtitleStyle.Font, SizePx: s.SubtitleStyle.SizePx,
			Position: gen.RenderSubtitleStylePosition(s.SubtitleStyle.Position), ShadowPx: s.SubtitleStyle.ShadowPx,
		},
		DefaultMotion: gen.MotionPreset(s.DefaultMotion), CrossfadeMs: s.CrossfadeMs,
		LoudnessLufs: float32(s.LoudnessLUFSx10) / 10, TruePeakDbtp: float32(s.TruePeakDBTPx10) / 10,
	}
}

func settingsFromAPI(a gen.RenderSettings) Settings {
	return Settings{
		Width: a.Width, Height: a.Height, FPS: int(a.Fps), Encoder: string(a.Encoder), Subtitles: string(a.Subtitles),
		SubtitleStyle: SubtitleStyle{
			Font: a.SubtitleStyle.Font, SizePx: a.SubtitleStyle.SizePx,
			Position: string(a.SubtitleStyle.Position), ShadowPx: a.SubtitleStyle.ShadowPx,
		},
		DefaultMotion: string(a.DefaultMotion), CrossfadeMs: a.CrossfadeMs,
		LoudnessLUFSx10: int(math.Round(float64(a.LoudnessLufs) * 10)),
		TruePeakDBTPx10: int(math.Round(float64(a.TruePeakDbtp) * 10)),
	}
}
