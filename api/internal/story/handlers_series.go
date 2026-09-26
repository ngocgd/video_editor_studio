package story

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// CreateSeries implements gen.StrictServerInterface. Editor role is
// enforced by x-min-role; ownership is by construction (the row is
// created under the caller's own tenant).
func (h *StoryAPI) CreateSeries(ctx context.Context, req gen.CreateSeriesRequestObject) (gen.CreateSeriesResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	sess, _ := authpkg.FromCtx(ctx)
	body := req.Body

	langs := make([]string, 0, len(body.TargetLanguages))
	for _, l := range body.TargetLanguages {
		langs = append(langs, string(l))
	}
	if len(langs) == 0 {
		langs = []string{"en"}
	}

	var genre, styleNotes string
	if body.Genre != nil {
		genre = *body.Genre
	}
	if body.StyleNotes != nil {
		styleNotes = *body.StyleNotes
	}

	s, err := h.Queries.CreateSeries(ctx, dbgen.CreateSeriesParams{
		ID:                   idconv.ToPg(idconv.NewV7()),
		TenantID:             idconv.ToPg(info.ID),
		Title:                body.Title,
		Genre:                genre,
		TargetLanguages:      langs,
		TargetEpisodeMinutes: int32(body.TargetEpisodeMinutes),
		PlannedEpisodeCount:  int32(body.PlannedEpisodeCount),
		StyleNotes:           styleNotes,
		CreatedBy:            idconv.ToPg(sess.UserID),
	})
	if err != nil {
		return nil, err
	}

	// Every series gets an (initially empty) story bible row up front, so
	// GetBible never needs to distinguish "not created yet" from "not
	// found" for a series that does exist.
	emptySections, _ := json.Marshal(map[string]bibleSectionDoc{})
	if _, err := h.Queries.CreateStoryBible(ctx, dbgen.CreateStoryBibleParams{
		ID:       idconv.ToPg(idconv.NewV7()),
		TenantID: idconv.ToPg(info.ID),
		SeriesID: s.ID,
		Sections: emptySections,
	}); err != nil {
		return nil, err
	}

	return gen.CreateSeries201JSONResponse(seriesToDTO(s)), nil
}

// ListSeries implements gen.StrictServerInterface.
func (h *StoryAPI) ListSeries(ctx context.Context, req gen.ListSeriesRequestObject) (gen.ListSeriesResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	limit := int32(defaultPageLimit)
	if req.Params.Limit != nil && *req.Params.Limit > 0 {
		limit = int32(*req.Params.Limit)
	}
	var cursor uuid.UUID
	if req.Params.Cursor != nil {
		if id, err := uuid.Parse(*req.Params.Cursor); err == nil {
			cursor = id
		}
	}
	rows, err := h.Queries.ListSeries(ctx, dbgen.ListSeriesParams{TenantID: idconv.ToPg(info.ID), Cursor: idconv.ToPg(cursor), PageLimit: limit})
	if err != nil {
		return nil, err
	}
	items := make([]gen.Series, 0, len(rows))
	for _, s := range rows {
		items = append(items, seriesToDTO(s))
	}
	var nextCursor *string
	if len(rows) == int(limit) && len(rows) > 0 {
		c := idconv.FromPg(rows[len(rows)-1].ID).String()
		nextCursor = &c
	}
	return gen.ListSeries200JSONResponse{Items: items, NextCursor: nextCursor}, nil
}

// GetSeries implements gen.StrictServerInterface.
func (h *StoryAPI) GetSeries(ctx context.Context, req gen.GetSeriesRequestObject) (gen.GetSeriesResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	s, err := h.requireSeries(ctx, info.ID, req.Id)
	if err != nil {
		if isNoRows(err) {
			return gen.GetSeries404ApplicationProblemPlusJSONResponse{Title: "series not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}
	return gen.GetSeries200JSONResponse(seriesToDTO(s)), nil
}

// UpdateSeries implements gen.StrictServerInterface.
func (h *StoryAPI) UpdateSeries(ctx context.Context, req gen.UpdateSeriesRequestObject) (gen.UpdateSeriesResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, err := h.requireSeries(ctx, info.ID, req.Id); err != nil {
		if isNoRows(err) {
			return gen.UpdateSeries404ApplicationProblemPlusJSONResponse{Title: "series not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}

	body := req.Body
	langs := make([]string, 0, len(body.TargetLanguages))
	for _, l := range body.TargetLanguages {
		langs = append(langs, string(l))
	}
	status := "draft"
	if body.Status != nil {
		status = string(*body.Status)
	}
	var genre, styleNotes string
	if body.Genre != nil {
		genre = *body.Genre
	}
	if body.StyleNotes != nil {
		styleNotes = *body.StyleNotes
	}

	s, err := h.Queries.UpdateSeries(ctx, dbgen.UpdateSeriesParams{
		Title:                body.Title,
		Genre:                genre,
		TargetLanguages:      langs,
		TargetEpisodeMinutes: int32(body.TargetEpisodeMinutes),
		PlannedEpisodeCount:  int32(body.PlannedEpisodeCount),
		StyleNotes:           styleNotes,
		Status:               status,
		TenantID:             idconv.ToPg(info.ID),
		ID:                   idconv.ToPg(req.Id),
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateSeries200JSONResponse(seriesToDTO(s)), nil
}

// GenerateSeries implements gen.StrictServerInterface: kicks off the
// settings -> bible seed -> episode outlines wizard as one pipeline run.
// The bible-seed step has no dependency; each outline step depends on the
// bible-seed step finishing (the outline prompt reads the freshly
// generated bible).
func (h *StoryAPI) GenerateSeries(ctx context.Context, req gen.GenerateSeriesRequestObject) (gen.GenerateSeriesResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	sess, _ := authpkg.FromCtx(ctx)
	series, err := h.requireSeries(ctx, info.ID, req.Id)
	if err != nil {
		if isNoRows(err) {
			return gen.GenerateSeries404ApplicationProblemPlusJSONResponse{Title: "series not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}

	episodeCount := int(series.PlannedEpisodeCount)
	if req.Body != nil && req.Body.EpisodeCount != nil {
		episodeCount = *req.Body.EpisodeCount
	}
	if episodeCount < 1 {
		episodeCount = 1
	}

	seriesID := idconv.FromPg(series.ID)
	runID := idconv.NewV7()
	bibleSeedStepID := idconv.NewV7()
	steps := []pipeline.StepSpec{{
		ID:        bibleSeedStepID,
		Kind:      KindBibleSeed,
		ScopeKind: ScopeSeries,
		ScopeID:   seriesID,
		Priority:  pipeline.PriorityInteractive,
	}}
	for i := 0; i < episodeCount; i++ {
		steps = append(steps, pipeline.StepSpec{
			ID:        idconv.NewV7(),
			Kind:      KindOutline,
			ScopeKind: ScopeSeries,
			ScopeID:   seriesID,
			Priority:  pipeline.PriorityInteractive,
			DependsOn: []uuid.UUID{bibleSeedStepID},
		})
	}

	if _, err := h.Engine.Enqueue(ctx, info.ID, pipeline.RunSpec{
		ID:        runID,
		ScopeKind: ScopeSeries,
		ScopeID:   seriesID,
		Kind:      "series.generate",
		CreatedBy: userIDPtr(sess),
		Steps:     steps,
	}); err != nil {
		return nil, err
	}

	return gen.GenerateSeries202JSONResponse{RunId: runID}, nil
}
