package story

import (
	"context"
	"encoding/json"
	"fmt"
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

// CreateDraft implements gen.StrictServerInterface: PUT create-if-absent.
// The writer shows a "Create draft" button instead of a dead-end "No draft"
// message for an outlined or manually created episode (which has no draft
// until something writes one); this is that write. A draft that already
// exists is returned unchanged rather than reset.
func (h *StoryAPI) CreateDraft(ctx context.Context, req gen.CreateDraftRequestObject) (gen.CreateDraftResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, err := h.requireEpisode(ctx, info.ID, req.Id); err != nil {
		if isNoRows(err) {
			return gen.CreateDraft404ApplicationProblemPlusJSONResponse{Title: "episode not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}

	created, err := h.Queries.CreateDraftIfAbsent(ctx, dbgen.CreateDraftIfAbsentParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(info.ID), EpisodeID: idconv.ToPg(req.Id),
		Lang: string(req.Lang), Paragraphs: []byte("[]"), WordCount: 0,
	})
	if err == nil {
		dto, err := draftToDTO(created)
		if err != nil {
			return nil, err
		}
		return gen.CreateDraft201JSONResponse(dto), nil
	}
	if !isNoRows(err) {
		return nil, err
	}
	existing, err := h.Queries.GetDraft(ctx, dbgen.GetDraftParams{TenantID: idconv.ToPg(info.ID), EpisodeID: idconv.ToPg(req.Id), Lang: string(req.Lang)})
	if err != nil {
		return nil, err
	}
	dto, err := draftToDTO(existing)
	if err != nil {
		return nil, err
	}
	return gen.CreateDraft200JSONResponse(dto), nil
}

// ApplyDraftStep implements gen.StrictServerInterface: applies a done AI
// action step's own output to the draft, computing origin/taint
// server-side from the step rather than trusting whatever the client
// copied into a generic PATCH upsert (which is how taint and provenance
// used to get silently dropped on every AI accept). Continue and
// expand_beat insert their text as new paragraphs after the anchor and
// never delete; every other action (rewrite/expand/shorten/tone/translate)
// replaces the selected paragraphs.
func (h *StoryAPI) ApplyDraftStep(ctx context.Context, req gen.ApplyDraftStepRequestObject) (gen.ApplyDraftStepResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	sess, _ := authpkg.FromCtx(ctx)
	if _, err := h.requireEpisode(ctx, info.ID, req.Id); err != nil {
		if isNoRows(err) {
			return gen.ApplyDraftStep404ApplicationProblemPlusJSONResponse{Title: "episode not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}

	step, err := h.Queries.GetStepByID(ctx, dbgen.GetStepByIDParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Body.StepId)})
	if err != nil {
		if isNoRows(err) {
			return gen.ApplyDraftStep404ApplicationProblemPlusJSONResponse{Title: "step not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}
	// Same scoping rule as GetAiActionResult: the step must be an AI action
	// on this very episode, not just any tenant-scoped step id.
	if step.ScopeKind != ScopeEpisode || idconv.FromPg(step.ScopeID) != req.Id || !isAiActionKind(step.Kind) {
		return gen.ApplyDraftStep404ApplicationProblemPlusJSONResponse{Title: "step not found", Status: http.StatusNotFound}, nil
	}
	if step.Status != "done" {
		detail := "the step has not finished yet"
		return gen.ApplyDraftStep409ApplicationProblemPlusJSONResponse{Title: "step not done", Status: http.StatusConflict, Detail: &detail}, nil
	}
	var out aiActionOutput
	if err := json.Unmarshal(step.Output, &out); err != nil || out.Text == "" {
		detail := "the step has no output to apply"
		return gen.ApplyDraftStep409ApplicationProblemPlusJSONResponse{Title: "nothing to apply", Status: http.StatusConflict, Detail: &detail}, nil
	}
	var in AiActionInput
	_ = json.Unmarshal(step.Input, &in) // best effort; an empty/legacy Input just falls back to "replace"

	// A translation belongs to its target-language draft, created on first
	// use, and is appended there. The selection it was made from lives in
	// the source draft, so it is not looked up here.
	translating := in.Action == "translate"
	if translating {
		if string(req.Lang) != out.Lang {
			detail := fmt.Sprintf("this translation is for the %s draft", out.Lang)
			return gen.ApplyDraftStep409ApplicationProblemPlusJSONResponse{Title: "wrong draft", Status: http.StatusConflict, Detail: &detail}, nil
		}
		if _, _, err := getOrCreateDraft(ctx, h.Queries, info.ID, req.Id, out.Lang); err != nil {
			return nil, err
		}
	}

	current, err := h.Queries.GetDraft(ctx, dbgen.GetDraftParams{TenantID: idconv.ToPg(info.ID), EpisodeID: idconv.ToPg(req.Id), Lang: string(req.Lang)})
	if err != nil {
		if isNoRows(err) {
			detail := "draft does not exist yet"
			return gen.ApplyDraftStep409ApplicationProblemPlusJSONResponse{Title: "draft not found", Status: http.StatusConflict, Detail: &detail}, nil
		}
		return nil, err
	}
	paragraphs, err := decodeParagraphs(current.Paragraphs)
	if err != nil {
		return nil, err
	}

	var ids []string
	if req.Body.ParagraphIds != nil && !translating {
		ids = *req.Body.ParagraphIds
	}
	inserting := translating || in.Action == "continue" || in.Action == "expand_beat"
	// Every referenced paragraph must still exist, and a replacing action
	// needs a selection: without one it would replace the whole draft.
	missing := len(ids) > 0 && len(selectParagraphs(paragraphs, ids)) != len(ids)
	if missing || (!inserting && len(ids) == 0 && len(paragraphs) > 0) {
		detail := "the selected paragraphs are not in the current draft; reload and retry"
		return gen.ApplyDraftStep409ApplicationProblemPlusJSONResponse{Title: "invalid paragraph reference", Status: http.StatusConflict, Detail: &detail}, nil
	}
	resultTainted := out.Tainted
	if len(ids) > 0 {
		resultTainted = resultTainted || anyParagraphTainted(selectParagraphs(paragraphs, ids))
	}
	newParagraphs := paragraphsFromText(out.Text, resultTainted)

	var updated []Paragraph
	if inserting {
		anchor := ""
		switch {
		case req.Body.AfterParagraphId != nil && !translating:
			anchor = *req.Body.AfterParagraphId
		case len(ids) > 0:
			anchor = ids[len(ids)-1]
		case len(paragraphs) > 0:
			anchor = paragraphs[len(paragraphs)-1].ID
		}
		updated, err = insertParagraphsAfter(paragraphs, newParagraphs, anchor)
		if err != nil {
			detail := err.Error()
			return gen.ApplyDraftStep409ApplicationProblemPlusJSONResponse{Title: "invalid paragraph reference", Status: http.StatusConflict, Detail: &detail}, nil
		}
	} else {
		updated = replaceParagraphs(paragraphs, ids, newParagraphs)
	}

	stepID := req.Body.StepId
	saved, err := h.commitDraftWrite(ctx, draftWrite{
		tenantID: info.ID, current: current, paragraphs: updated, userID: userIDPtr(sess), stepID: &stepID,
	})
	if err != nil {
		if title := draftWriteConflictTitle(err); title != "" {
			detail := err.Error()
			return gen.ApplyDraftStep409ApplicationProblemPlusJSONResponse{Title: title, Status: http.StatusConflict, Detail: &detail}, nil
		}
		return nil, err
	}

	dto, err := draftToDTO(saved)
	if err != nil {
		return nil, err
	}
	return gen.ApplyDraftStep200JSONResponse(dto), nil
}

// PatchDraft implements gen.StrictServerInterface: the paragraph-ops CAS.
// It loads the current draft, applies every op server-side to a decoded
// Go slice (never a DB-side JSON path hack), and writes the result with
// UpdateDraftParagraphs' own WHERE version=expectedVersion fence, which
// is the actual optimistic-concurrency check — a concurrent writer's
// UPDATE affects zero rows and is reported here as 409. A revision row is
// inserted (and old ones trimmed to 50) in the same transaction as the
// write (see commitDraftWrite).
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

	saved, err := h.commitDraftWrite(ctx, draftWrite{
		tenantID: info.ID, current: current, paragraphs: updated, userID: userIDPtr(sess), stepID: nil,
	})
	if err != nil {
		if title := draftWriteConflictTitle(err); title != "" {
			detail := err.Error()
			return gen.PatchDraft409ApplicationProblemPlusJSONResponse{Title: title, Status: http.StatusConflict, Detail: &detail}, nil
		}
		return nil, err
	}

	dto, err := draftToDTO(saved)
	if err != nil {
		return nil, err
	}
	return gen.PatchDraft200JSONResponse(dto), nil
}
