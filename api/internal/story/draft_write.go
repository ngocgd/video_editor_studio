package story

import (
	"context"
	"errors"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// maxDraftParagraphs caps how many paragraphs one draft may hold. A
// 30-minute episode is well under a hundred paragraphs; the cap only stops
// repeated writes from growing a draft (and its revision history) without
// bound.
const maxDraftParagraphs = 5000

// Reasons a draft write is refused; each maps to a 409 in the handlers.
var (
	errDraftVersionConflict = errors.New("the draft was changed by someone else; reload and retry")
	errDraftTooLarge        = errors.New("the draft would exceed the paragraph limit")
	errStepAlreadyApplied   = errors.New("this result was already applied")
)

// draftWrite is one new version of a draft: the paragraphs replacing
// current's, who wrote them, and (for an accepted AI result) the step
// they came from.
type draftWrite struct {
	tenantID   uuid.UUID
	current    dbgen.EpisodeDraft
	paragraphs []Paragraph
	userID     *uuid.UUID
	stepID     *uuid.UUID
}

// commitDraftWrite stores w in one transaction: the version-fenced draft
// update, the revision row, trimming old revisions and, for an AI result,
// the claim that its step has now been applied. Either all of it commits
// or none does, so a failed revision insert can no longer leave a
// committed draft behind a 500, and a step can be applied only once.
func (h *StoryAPI) commitDraftWrite(ctx context.Context, w draftWrite) (dbgen.EpisodeDraft, error) {
	if len(w.paragraphs) > maxDraftParagraphs {
		return dbgen.EpisodeDraft{}, errDraftTooLarge
	}
	paragraphsJSON, err := encodeParagraphs(w.paragraphs)
	if err != nil {
		return dbgen.EpisodeDraft{}, err
	}
	wordCount := int32(WordCount(w.paragraphs))
	nextVersion := w.current.Version + 1
	tenantID := idconv.ToPg(w.tenantID)

	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return dbgen.EpisodeDraft{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	if w.stepID != nil {
		claimed, err := qtx.ClaimDraftStepApplication(ctx, dbgen.ClaimDraftStepApplicationParams{
			StepID: idconv.ToPg(*w.stepID), TenantID: tenantID, DraftID: w.current.ID,
			DraftVersion: nextVersion, AppliedBy: idconv.ToPgPtr(w.userID),
		})
		if err != nil {
			return dbgen.EpisodeDraft{}, err
		}
		if claimed == 0 {
			return dbgen.EpisodeDraft{}, errStepAlreadyApplied
		}
	}

	saved, err := qtx.UpdateDraftParagraphs(ctx, dbgen.UpdateDraftParagraphsParams{
		Paragraphs: paragraphsJSON, WordCount: wordCount, NextVersion: nextVersion,
		TenantID: tenantID, ID: w.current.ID, ExpectedVersion: w.current.Version,
	})
	if err != nil {
		if isNoRows(err) {
			return dbgen.EpisodeDraft{}, errDraftVersionConflict
		}
		return dbgen.EpisodeDraft{}, err
	}
	if err := qtx.InsertDraftRevision(ctx, dbgen.InsertDraftRevisionParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: tenantID, DraftID: w.current.ID,
		Version: nextVersion, Paragraphs: paragraphsJSON, WordCount: wordCount, CreatedBy: idconv.ToPgPtr(w.userID),
	}); err != nil {
		return dbgen.EpisodeDraft{}, err
	}
	if err := qtx.TrimDraftRevisions(ctx, dbgen.TrimDraftRevisionsParams{TenantID: tenantID, DraftID: w.current.ID}); err != nil {
		return dbgen.EpisodeDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return dbgen.EpisodeDraft{}, err
	}
	return saved, nil
}

// draftWriteConflictTitle returns the problem title for a refused draft
// write, or "" when err is not one of the refusal reasons.
func draftWriteConflictTitle(err error) string {
	switch {
	case errors.Is(err, errDraftVersionConflict):
		return "version conflict"
	case errors.Is(err, errDraftTooLarge):
		return "draft too large"
	case errors.Is(err, errStepAlreadyApplied):
		return "already applied"
	}
	return ""
}
