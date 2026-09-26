package story

import (
	"encoding/json"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/duration"
	"loomtale/api/internal/httpapi/gen"
)

func seriesToDTO(s dbgen.Series) gen.Series {
	dto := gen.Series{
		Id:                   idconv.FromPg(s.ID),
		Title:                s.Title,
		PlannedEpisodeCount:  int(s.PlannedEpisodeCount),
		TargetEpisodeMinutes: int(s.TargetEpisodeMinutes),
		Status:               gen.SeriesStatus(s.Status),
		CreatedAt:            idconv.FromPgTimestamptz(s.CreatedAt),
	}
	for _, l := range s.TargetLanguages {
		dto.TargetLanguages = append(dto.TargetLanguages, gen.TargetLanguage(l))
	}
	if s.Genre != "" {
		dto.Genre = &s.Genre
	}
	if s.StyleNotes != "" {
		dto.StyleNotes = &s.StyleNotes
	}
	return dto
}

// bibleSectionDoc is the per-section shape stored in story_bibles.sections
// jsonb, matching db/migrations' comment and the BibleSection DTO.
type bibleSectionDoc struct {
	Content string `json:"content"`
	Origin  string `json:"origin"`
	Tainted bool   `json:"tainted"`
	Version int    `json:"version"`
}

func decodeBibleSections(raw []byte) (map[string]bibleSectionDoc, error) {
	sections := map[string]bibleSectionDoc{}
	if len(raw) == 0 {
		return sections, nil
	}
	if err := json.Unmarshal(raw, &sections); err != nil {
		return nil, err
	}
	return sections, nil
}

func bibleToDTO(b dbgen.StoryBible) (gen.StoryBible, error) {
	sections, err := decodeBibleSections(b.Sections)
	if err != nil {
		return gen.StoryBible{}, err
	}
	dto := gen.StoryBible{
		SeriesId:  idconv.FromPg(b.SeriesID),
		UpdatedAt: idconv.FromPgTimestamptz(b.UpdatedAt),
		Sections:  map[string]gen.BibleSection{},
	}
	for name, s := range sections {
		dto.Sections[name] = gen.BibleSection{Content: s.Content, Origin: gen.Origin(s.Origin), Tainted: s.Tainted, Version: s.Version}
	}
	return dto, nil
}

// outlineBeatDoc is the per-beat shape stored in episodes.outline jsonb.
// Tainted is set once, at outline generation time, from whether the bible
// excerpt fed into that llm.outline call was itself tainted (cheap to
// record then; there is no per-beat provenance beyond that).
type outlineBeatDoc struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	TargetWords int    `json:"targetWords"`
	Tainted     bool   `json:"tainted,omitempty"`
}

func decodeOutline(raw []byte) ([]outlineBeatDoc, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var beats []outlineBeatDoc
	if err := json.Unmarshal(raw, &beats); err != nil {
		return nil, err
	}
	return beats, nil
}

func episodeToDTO(e dbgen.Episode) (gen.Episode, error) {
	beats, err := decodeOutline(e.Outline)
	if err != nil {
		return gen.Episode{}, err
	}
	dto := gen.Episode{
		Id:        idconv.FromPg(e.ID),
		SeriesId:  idconv.FromPg(e.SeriesID),
		Idx:       int(e.Idx),
		Title:     e.Title,
		Status:    gen.EpisodeStatus(e.Status),
		CreatedAt: idconv.FromPgTimestamptz(e.CreatedAt),
		Outline:   make([]gen.OutlineBeat, 0, len(beats)),
	}
	for _, b := range beats {
		tainted := b.Tainted
		dto.Outline = append(dto.Outline, gen.OutlineBeat{Id: b.ID, Summary: b.Summary, TargetWords: b.TargetWords, Tainted: &tainted})
	}
	return dto, nil
}

func decodeParagraphs(raw []byte) ([]Paragraph, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var paragraphs []Paragraph
	if err := json.Unmarshal(raw, &paragraphs); err != nil {
		return nil, err
	}
	return paragraphs, nil
}

func draftToDTO(d dbgen.EpisodeDraft) (gen.EpisodeDraft, error) {
	paragraphs, err := decodeParagraphs(d.Paragraphs)
	if err != nil {
		return gen.EpisodeDraft{}, err
	}
	dto := gen.EpisodeDraft{
		EpisodeId:  idconv.FromPg(d.EpisodeID),
		Lang:       gen.DraftLanguage(d.Lang),
		Version:    int(d.Version),
		WordCount:  int(d.WordCount),
		Paragraphs: make([]gen.DraftParagraph, 0, len(paragraphs)),
	}
	for _, p := range paragraphs {
		dto.Paragraphs = append(dto.Paragraphs, gen.DraftParagraph{Id: p.ID, Text: p.Text, Origin: gen.Origin(p.Origin), Tainted: p.Tainted})
	}
	if minutes, ok := estimateMinutes(int(d.WordCount), d.Lang); ok {
		dto.DurationEstimateMinutes = &minutes
	}
	if d.Summary != "" {
		dto.Summary = &d.Summary
		tainted := d.SummaryTainted
		dto.SummaryTainted = &tainted
	}
	return dto, nil
}

func encodeParagraphs(paragraphs []Paragraph) ([]byte, error) {
	if paragraphs == nil {
		paragraphs = []Paragraph{}
	}
	return json.Marshal(paragraphs)
}

// estimateMinutes is the spoken-duration estimate for a draft, from
// duration.Estimate. Only the narrated languages (en, vi) get one: a zh
// source draft counts characters, so a words-per-minute rate means
// nothing for it.
func estimateMinutes(words int, lang string) (float32, bool) {
	if lang != "en" && lang != "vi" {
		return 0, false
	}
	return float32(duration.Estimate(words, lang, "").Minutes), true
}

// durationEstimates maps each narrated draft language to its estimate,
// for the episode list.
func durationEstimates(drafts map[string]gen.DraftStatus) *map[string]float32 {
	out := map[string]float32{}
	for lang, status := range drafts {
		if status.WordCount == nil {
			continue
		}
		if minutes, ok := estimateMinutes(*status.WordCount, lang); ok {
			out[lang] = minutes
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &out
}
