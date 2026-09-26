package story

import (
	"context"
	"encoding/json"
	"net/http"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/tenant"
)

const defaultPageLimit = 50

// ListEpisodes implements gen.StrictServerInterface, aggregating draft
// word counts/versions in one query (no N+1, per the performance budget).
func (h *StoryAPI) ListEpisodes(ctx context.Context, req gen.ListEpisodesRequestObject) (gen.ListEpisodesResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	limit := int32(defaultPageLimit)
	if req.Params.Limit != nil && *req.Params.Limit > 0 {
		limit = int32(*req.Params.Limit)
	}
	var cursor int32
	if req.Params.Cursor != nil {
		cursor = decodeCursorInt32(*req.Params.Cursor)
	}

	rows, err := h.Queries.ListEpisodesWithDraftStatus(ctx, dbgen.ListEpisodesWithDraftStatusParams{
		TenantID: idconv.ToPg(info.ID), SeriesID: idconv.ToPg(req.Params.SeriesId), Cursor: cursor, PageLimit: limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]gen.Episode, 0, len(rows))
	var nextCursor *string
	for _, r := range rows {
		dto, err := episodeToDTO(dbgen.Episode{
			ID: r.ID, TenantID: r.TenantID, SeriesID: r.SeriesID, Idx: r.Idx, Title: r.Title,
			Outline: r.Outline, Status: r.Status, SourceImportChapterIndex: r.SourceImportChapterIndex,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
		if err != nil {
			return nil, err
		}
		if drafts, ok := r.Drafts.(map[string]any); ok {
			dto.Drafts = draftStatusMap(drafts)
			dto.DurationEstimateMinutes = durationEstimates(*dto.Drafts)
		}
		items = append(items, dto)
	}
	if len(rows) == int(limit) && len(rows) > 0 {
		c := encodeCursorInt32(rows[len(rows)-1].Idx)
		nextCursor = &c
	}
	return gen.ListEpisodes200JSONResponse{Items: items, NextCursor: nextCursor}, nil
}

// CreateEpisode implements gen.StrictServerInterface: a manually created
// (not LLM-outlined) episode, starting with an empty outline.
func (h *StoryAPI) CreateEpisode(ctx context.Context, req gen.CreateEpisodeRequestObject) (gen.CreateEpisodeResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	// The series must belong to the caller's tenant: episode (series_id,
	// idx) is unique across tenants, so a foreign episode would take index
	// slots in someone else's series.
	if _, err := h.requireSeries(ctx, info.ID, req.Params.SeriesId); err != nil {
		if isNoRows(err) {
			return gen.CreateEpisode404ApplicationProblemPlusJSONResponse{Title: "series not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}
	idx, err := h.Queries.NextEpisodeIdx(ctx, dbgen.NextEpisodeIdxParams{TenantID: idconv.ToPg(info.ID), SeriesID: idconv.ToPg(req.Params.SeriesId)})
	if err != nil {
		return nil, err
	}
	e, err := h.Queries.CreateEpisode(ctx, dbgen.CreateEpisodeParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(info.ID), SeriesID: idconv.ToPg(req.Params.SeriesId),
		Idx: idx, Title: "Untitled episode", Outline: []byte("[]"), Status: "planned",
	})
	if err != nil {
		return nil, err
	}
	dto, err := episodeToDTO(e)
	if err != nil {
		return nil, err
	}
	return gen.CreateEpisode201JSONResponse(dto), nil
}

// GetEpisode implements gen.StrictServerInterface.
func (h *StoryAPI) GetEpisode(ctx context.Context, req gen.GetEpisodeRequestObject) (gen.GetEpisodeResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	e, err := h.requireEpisode(ctx, info.ID, req.Id)
	if err != nil {
		if isNoRows(err) {
			return gen.GetEpisode404ApplicationProblemPlusJSONResponse{Title: "episode not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}
	dto, err := episodeToDTO(e)
	if err != nil {
		return nil, err
	}
	return gen.GetEpisode200JSONResponse(dto), nil
}

// UpdateEpisode implements gen.StrictServerInterface: title/status only
// (the outline is only ever written by llm.outline/llm.expand_beat).
func (h *StoryAPI) UpdateEpisode(ctx context.Context, req gen.UpdateEpisodeRequestObject) (gen.UpdateEpisodeResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	existing, err := h.requireEpisode(ctx, info.ID, req.Id)
	if err != nil {
		if isNoRows(err) {
			return gen.UpdateEpisode404ApplicationProblemPlusJSONResponse{Title: "episode not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}

	title := existing.Title
	if req.Body.Title != nil {
		title = *req.Body.Title
	}
	status := existing.Status
	if req.Body.Status != nil {
		status = string(*req.Body.Status)
	}

	e, err := h.Queries.UpdateEpisodeMeta(ctx, dbgen.UpdateEpisodeMetaParams{Title: title, Status: status, TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)})
	if err != nil {
		return nil, err
	}
	dto, err := episodeToDTO(e)
	if err != nil {
		return nil, err
	}
	return gen.UpdateEpisode200JSONResponse(dto), nil
}

func draftStatusMap(raw map[string]any) *map[string]gen.DraftStatus {
	out := map[string]gen.DraftStatus{}
	for lang, v := range raw {
		obj, ok := v.(map[string]any)
		if !ok {
			continue
		}
		status := gen.DraftStatus{}
		if wc, ok := obj["wordCount"].(float64); ok {
			n := int(wc)
			status.WordCount = &n
		}
		if ver, ok := obj["version"].(float64); ok {
			n := int(ver)
			status.Version = &n
		}
		out[lang] = status
	}
	return &out
}

func decodeCursorInt32(s string) int32 {
	var n int32
	_ = json.Unmarshal([]byte(s), &n)
	return n
}

func encodeCursorInt32(n int32) string {
	b, _ := json.Marshal(n)
	return string(b)
}
