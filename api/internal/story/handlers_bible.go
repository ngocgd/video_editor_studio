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

// GetBible implements gen.StrictServerInterface. req.Id is the series id
// (per openapi/paths/series.yaml's /series/{id}/bible route).
func (h *StoryAPI) GetBible(ctx context.Context, req gen.GetBibleRequestObject) (gen.GetBibleResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, err := h.requireSeries(ctx, info.ID, req.Id); err != nil {
		if isNoRows(err) {
			return gen.GetBible404ApplicationProblemPlusJSONResponse{Title: "series not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}
	bible, err := h.Queries.GetStoryBible(ctx, dbgen.GetStoryBibleParams{TenantID: idconv.ToPg(info.ID), SeriesID: idconv.ToPg(req.Id)})
	if err != nil {
		if isNoRows(err) {
			return gen.GetBible404ApplicationProblemPlusJSONResponse{Title: "bible not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}
	dto, err := bibleToDTO(bible)
	if err != nil {
		return nil, err
	}
	return gen.GetBible200JSONResponse(dto), nil
}

// UpdateBibleSection implements gen.StrictServerInterface: a
// read-modify-write of the whole sections jsonb map, CAS-checked against
// the target section's own current version (Postgres has no per-key jsonb
// CAS, see the UpdateStoryBibleSections query comment). A human edit of a
// tainted section keeps its taint; editing sets origin=user only when the
// section did not exist yet.
func (h *StoryAPI) UpdateBibleSection(ctx context.Context, req gen.UpdateBibleSectionRequestObject) (gen.UpdateBibleSectionResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	// UpdateBibleSection's contract declares only 200/409 (no 404): a
	// missing series is reported as 409 with a distinguishing title.
	if _, err := h.requireSeries(ctx, info.ID, req.Id); err != nil {
		if isNoRows(err) {
			return gen.UpdateBibleSection409ApplicationProblemPlusJSONResponse{Title: "series not found", Status: http.StatusConflict}, nil
		}
		return nil, err
	}
	bible, err := h.Queries.GetStoryBible(ctx, dbgen.GetStoryBibleParams{TenantID: idconv.ToPg(info.ID), SeriesID: idconv.ToPg(req.Id)})
	if err != nil {
		return nil, err
	}
	sections, err := decodeBibleSections(bible.Sections)
	if err != nil {
		return nil, err
	}

	body := req.Body
	name := string(body.Section)
	current, exists := sections[name]
	if exists && current.Version != body.ExpectedVersion {
		detail := "the section was changed by someone else; reload and retry"
		return gen.UpdateBibleSection409ApplicationProblemPlusJSONResponse{Title: "version conflict", Status: http.StatusConflict, Detail: &detail}, nil
	}
	if !exists && body.ExpectedVersion != 0 {
		detail := "the section does not exist yet; expectedVersion must be 0"
		return gen.UpdateBibleSection409ApplicationProblemPlusJSONResponse{Title: "version conflict", Status: http.StatusConflict, Detail: &detail}, nil
	}

	next := bibleSectionDoc{Content: body.Content, Origin: "user", Tainted: current.Tainted, Version: current.Version + 1}
	sections[name] = next

	raw, err := json.Marshal(sections)
	if err != nil {
		return nil, err
	}
	updated, err := h.Queries.UpdateStoryBibleSections(ctx, dbgen.UpdateStoryBibleSectionsParams{Sections: raw, TenantID: idconv.ToPg(info.ID), SeriesID: idconv.ToPg(req.Id)})
	if err != nil {
		return nil, err
	}
	dto, err := bibleToDTO(updated)
	if err != nil {
		return nil, err
	}
	return gen.UpdateBibleSection200JSONResponse(dto), nil
}
