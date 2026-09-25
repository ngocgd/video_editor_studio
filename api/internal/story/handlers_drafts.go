package story

import (
	"context"
	"net/http"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/tenant"
)

// GetDraft implements gen.StrictServerInterface. req.Id is the episode
// id (per openapi/paths/episodes.yaml's /episodes/{id}/drafts/{lang}
// route).
func (h *StoryAPI) GetDraft(ctx context.Context, req gen.GetDraftRequestObject) (gen.GetDraftResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, err := h.requireEpisode(ctx, info.ID, req.Id); err != nil {
		if isNoRows(err) {
			return gen.GetDraft404ApplicationProblemPlusJSONResponse{Title: "episode not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}
	d, err := h.Queries.GetDraft(ctx, dbgen.GetDraftParams{TenantID: idconv.ToPg(info.ID), EpisodeID: idconv.ToPg(req.Id), Lang: string(req.Lang)})
	if err != nil {
		if isNoRows(err) {
			return gen.GetDraft404ApplicationProblemPlusJSONResponse{Title: "draft not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}
	dto, err := draftToDTO(d)
	if err != nil {
		return nil, err
	}
	return gen.GetDraft200JSONResponse(dto), nil
}

// PatchDraft implements gen.StrictServerInterface: the paragraph-ops CAS.
// It loads the current draft, applies every op server-side to a decoded
// Go slice (never a DB-side JSON path hack), and writes the result with
// UpdateDraftParagraphs' own WHERE version=expectedVersion fence, which
// is the actual optimistic-concurrency check — a concurrent writer's
// UPDATE affects zero rows and is reported here as 409. A revision row is
// inserted (and old ones trimmed to 50) in the same request, after a
// successful write.
func (h *StoryAPI) PatchDraft(ctx context.Context, req gen.PatchDraftRequestObject) (gen.PatchDraftResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	sess, _ := authpkg.FromCtx(ctx)
	// PatchDraft's contract declares only 200/409 responses (no 404): a
	// missing episode or draft is reported as 409 with a distinguishing
	// title/detail rather than surfaced as a generic 500.
	if _, err := h.requireEpisode(ctx, info.ID, req.Id); err != nil {
		if isNoRows(err) {
			return gen.PatchDraft409ApplicationProblemPlusJSONResponse{Title: "episode not found", Status: http.StatusConflict}, nil
		}
		return nil, err
	}

	current, err := h.Queries.GetDraft(ctx, dbgen.GetDraftParams{TenantID: idconv.ToPg(info.ID), EpisodeID: idconv.ToPg(req.Id), Lang: string(req.Lang)})
	if err != nil {
		if isNoRows(err) {
			detail := "draft does not exist yet"
			return gen.PatchDraft409ApplicationProblemPlusJSONResponse{Title: "draft not found", Status: http.StatusConflict, Detail: &detail}, nil
		}
		return nil, err
	}
	if int(current.Version) != req.Body.ExpectedVersion {
		detail := "the draft was changed by someone else; reload and retry"
		return gen.PatchDraft409ApplicationProblemPlusJSONResponse{Title: "version conflict", Status: http.StatusConflict, Detail: &detail}, nil
	}

	paragraphs, err := decodeParagraphs(current.Paragraphs)
	if err != nil {
		return nil, err
	}

	ops := make([]ParagraphOp, 0, len(req.Body.Ops))
	for _, o := range req.Body.Ops {
		ops = append(ops, ParagraphOp{Op: string(o.Op), ParagraphID: o.ParagraphId, Text: o.Text, AfterParagraphID: o.AfterParagraphId})
	}
	updated, err := ApplyParagraphOps(paragraphs, ops)
	if err != nil {
		detail := err.Error()
		return gen.PatchDraft409ApplicationProblemPlusJSONResponse{Title: "invalid paragraph op", Status: http.StatusConflict, Detail: &detail}, nil
	}

	paragraphsJSON, err := encodeParagraphs(updated)
	if err != nil {
		return nil, err
	}
	wordCount := int32(WordCount(updated))
	nextVersion := current.Version + 1

	saved, err := h.Queries.UpdateDraftParagraphs(ctx, dbgen.UpdateDraftParagraphsParams{
		Paragraphs: paragraphsJSON, WordCount: wordCount, NextVersion: nextVersion,
		TenantID: idconv.ToPg(info.ID), ID: current.ID, ExpectedVersion: current.Version,
	})
	if err != nil {
		if isNoRows(err) {
			detail := "the draft was changed by someone else; reload and retry"
			return gen.PatchDraft409ApplicationProblemPlusJSONResponse{Title: "version conflict", Status: http.StatusConflict, Detail: &detail}, nil
		}
		return nil, err
	}

	if err := h.Queries.InsertDraftRevision(ctx, dbgen.InsertDraftRevisionParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(info.ID), DraftID: current.ID,
		Version: nextVersion, Paragraphs: paragraphsJSON, WordCount: wordCount, CreatedBy: idconv.ToPgPtr(userIDPtr(sess)),
	}); err != nil {
		return nil, err
	}
	if err := h.Queries.TrimDraftRevisions(ctx, current.ID); err != nil {
		return nil, err
	}

	dto, err := draftToDTO(saved)
	if err != nil {
		return nil, err
	}
	return gen.PatchDraft200JSONResponse(dto), nil
}
