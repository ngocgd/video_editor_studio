package story

import (
	"errors"
	"strings"
)

// Paragraph is one paragraph of a draft, matching the episode_drafts.
// paragraphs jsonb shape and the DraftParagraph DTO.
type Paragraph struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Origin  string `json:"origin"`
	Tainted bool   `json:"tainted"`
}

// ParagraphOp is one paragraph mutation from a PATCH request, matching
// gen.ParagraphOp.
type ParagraphOp struct {
	Op               string
	ParagraphID      string
	Text             *string
	AfterParagraphID *string
}

// ErrUnknownParagraph is returned when an op references a paragraph id
// that is not present in the current draft (delete/move) or, for upsert
// with AfterParagraphId set, whose "after" target is not present.
var ErrUnknownParagraph = errors.New("story: paragraph op references an unknown paragraph id")

// ErrDuplicateParagraph is returned when applying the ops would leave two
// paragraphs sharing one id.
var ErrDuplicateParagraph = errors.New("story: paragraph ops would duplicate a paragraph id")

// ErrInvalidOp is returned for an op with an unrecognized Op value or
// missing required fields (upsert without Text, move without
// AfterParagraphId).
var ErrInvalidOp = errors.New("story: invalid paragraph op")

// ApplyParagraphOps applies ops in order to a copy of current, returning
// the resulting paragraph slice. It never mutates current. A human edit
// (upsert of an existing paragraph id) never clears that paragraph's
// taint or origin; only inserting a brand new paragraph id starts fresh
// as origin=user, untainted.
func ApplyParagraphOps(current []Paragraph, ops []ParagraphOp) ([]Paragraph, error) {
	result := make([]Paragraph, len(current))
	copy(result, current)

	for _, op := range ops {
		var err error
		switch op.Op {
		case "upsert":
			result, err = applyUpsert(result, op)
		case "delete":
			result, err = applyDelete(result, op)
		case "move":
			result, err = applyMove(result, op)
		default:
			return nil, ErrInvalidOp
		}
		if err != nil {
			return nil, err
		}
	}
	// Every op addresses paragraphs by id, so a draft with an empty or
	// repeated id could never be edited reliably again.
	seen := make(map[string]struct{}, len(result))
	for _, p := range result {
		if p.ID == "" {
			return nil, ErrInvalidOp
		}
		if _, dup := seen[p.ID]; dup {
			return nil, ErrDuplicateParagraph
		}
		seen[p.ID] = struct{}{}
	}
	return result, nil
}

func applyUpsert(paragraphs []Paragraph, op ParagraphOp) ([]Paragraph, error) {
	if op.Text == nil {
		return nil, ErrInvalidOp
	}
	for i, p := range paragraphs {
		if p.ID == op.ParagraphID {
			// A human edit of previously tainted content never clears
			// taint or changes its recorded origin; only a distinct,
			// future "mark reviewed" action does.
			paragraphs[i].Text = *op.Text
			return paragraphs, nil
		}
	}
	// New paragraph: always origin=user, untainted (this op itself is
	// the human authoring it).
	newParagraph := Paragraph{ID: op.ParagraphID, Text: *op.Text, Origin: "user", Tainted: false}
	if op.AfterParagraphID == nil {
		return append(paragraphs, newParagraph), nil
	}
	return insertAfter(paragraphs, newParagraph, *op.AfterParagraphID)
}

func applyDelete(paragraphs []Paragraph, op ParagraphOp) ([]Paragraph, error) {
	for i, p := range paragraphs {
		if p.ID == op.ParagraphID {
			return append(paragraphs[:i:i], paragraphs[i+1:]...), nil
		}
	}
	return nil, ErrUnknownParagraph
}

func applyMove(paragraphs []Paragraph, op ParagraphOp) ([]Paragraph, error) {
	if op.AfterParagraphID == nil {
		return nil, ErrInvalidOp
	}
	idx := -1
	for i, p := range paragraphs {
		if p.ID == op.ParagraphID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, ErrUnknownParagraph
	}
	moved := paragraphs[idx]
	rest := append(paragraphs[:idx:idx], paragraphs[idx+1:]...)
	return insertAfter(rest, moved, *op.AfterParagraphID)
}

// insertAfter inserts p immediately after the paragraph with id afterID,
// or at the front when afterID is "". afterID must exist in paragraphs
// otherwise (unless it is "").
func insertAfter(paragraphs []Paragraph, p Paragraph, afterID string) ([]Paragraph, error) {
	if afterID == "" {
		out := make([]Paragraph, 0, len(paragraphs)+1)
		out = append(out, p)
		out = append(out, paragraphs...)
		return out, nil
	}
	for i, existing := range paragraphs {
		if existing.ID == afterID {
			out := make([]Paragraph, 0, len(paragraphs)+1)
			out = append(out, paragraphs[:i+1]...)
			out = append(out, p)
			out = append(out, paragraphs[i+1:]...)
			return out, nil
		}
	}
	return nil, ErrUnknownParagraph
}

// WordCount sums whitespace-separated words across every paragraph's
// text, matching the heuristic importer.wordCount uses elsewhere so a
// draft's word_count and an imported chapter's preview word count are
// computed the same way.
func WordCount(paragraphs []Paragraph) int {
	total := 0
	for _, p := range paragraphs {
		total += len(strings.Fields(p.Text))
	}
	return total
}
