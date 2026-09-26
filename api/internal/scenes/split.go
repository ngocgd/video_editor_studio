// Package scenes is the scene layer of an episode: splitting a draft into
// scenes (by paragraphs, or with the LLM), per-scene image, voice and
// subtitle-align steps with takes, stale detection by input hash, the
// storyboard rollup, and the scenes.Changed hook later phases subscribe
// to. The LLM only ever names characters; ids are resolved here.
package scenes

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/cases"

	"loomtale/api/internal/duration"
	"loomtale/api/internal/importer"
)

// Paragraph is one draft paragraph (episode_drafts.paragraphs entry).
type Paragraph struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Origin  string `json:"origin"`
	Tainted bool   `json:"tainted"`
}

// Segment is one voiced span of a scene. A nil speaker is the narrator.
type Segment struct {
	SpeakerCharacterID *uuid.UUID `json:"speakerCharacterId,omitempty"`
	Text               string     `json:"text"`
	UnrecognisedName   string     `json:"unrecognisedName,omitempty"`
}

// Span is a piece of paragraph text: narration, or a quoted line.
type Span struct {
	Text  string
	Quote bool
}

// quotePairs are the quotation marks dialogue is recognised by: straight
// and curly double quotes, and CJK corner brackets.
var quotePairs = map[rune]rune{'"': '"', '“': '”', '「': '」', '『': '』'}

// SplitSpans cuts text into narration and quoted spans, keeping the
// quotation marks on the quoted span. An unclosed quote runs to the end.
func SplitSpans(text string) []Span {
	var spans []Span
	var cur strings.Builder
	var closer rune
	inQuote := false
	flush := func(quote bool) {
		if s := strings.TrimSpace(cur.String()); s != "" {
			spans = append(spans, Span{Text: s, Quote: quote})
		}
		cur.Reset()
	}
	for _, r := range text {
		switch {
		case !inQuote:
			if c, ok := quotePairs[r]; ok {
				flush(false)
				inQuote, closer = true, c
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
			if r == closer {
				flush(true)
				inQuote = false
			}
		}
	}
	flush(inQuote)
	return spans
}

// NameIndex maps a case-folded character name, in any of its languages,
// to the character id. Only the series' own characters are in it, so a
// name can never resolve to another series' or tenant's character.
type NameIndex struct {
	byName map[string]uuid.UUID
	names  map[uuid.UUID][]string
}

// CharacterNames is one character's names for the index.
type CharacterNames struct {
	ID    uuid.UUID
	Names []string
}

var folder = cases.Fold()

func foldName(s string) string {
	return folder.String(strings.Join(strings.Fields(s), " "))
}

// NewNameIndex builds the index. A name shared by two characters is
// ambiguous and resolves to neither.
func NewNameIndex(chars []CharacterNames) NameIndex {
	idx := NameIndex{byName: map[string]uuid.UUID{}, names: map[uuid.UUID][]string{}}
	ambiguous := map[string]bool{}
	for _, c := range chars {
		for _, n := range c.Names {
			key := foldName(n)
			if key == "" {
				continue
			}
			if other, ok := idx.byName[key]; ok && other != c.ID {
				ambiguous[key] = true
				continue
			}
			idx.byName[key] = c.ID
			idx.names[c.ID] = append(idx.names[c.ID], strings.TrimSpace(n))
		}
	}
	for key := range ambiguous {
		delete(idx.byName, key)
	}
	return idx
}

// Resolve maps a name to a character id. The LLM's output is treated as
// a name only: a string that happens to be a UUID is looked up as a name
// like any other and never trusted as an id.
func (idx NameIndex) Resolve(name string) (uuid.UUID, bool) {
	id, ok := idx.byName[foldName(name)]
	return id, ok
}

// IsNarrator reports whether a speaker name means the narrator.
func IsNarrator(name string) bool {
	switch foldName(name) {
	case "", "narrator", "narration", "người dẫn chuyện", "旁白":
		return true
	}
	return false
}

// Speaker turns a model-emitted speaker name into a segment speaker: a
// known character, the narrator, or the narrator flagged with the
// unrecognised name for manual assignment.
func (idx NameIndex) Speaker(name string) (*uuid.UUID, string) {
	if IsNarrator(name) {
		return nil, ""
	}
	if id, ok := idx.Resolve(name); ok {
		return &id, ""
	}
	return nil, strings.TrimSpace(name)
}

// Mentioned returns the characters whose names occur in text, in index
// order of first mention.
func (idx NameIndex) Mentioned(text string) []uuid.UUID {
	folded := foldName(text)
	type hit struct {
		id  uuid.UUID
		pos int
	}
	var hits []hit
	for id, names := range idx.names {
		best := -1
		for _, n := range names {
			if p := indexWord(folded, foldName(n)); p >= 0 && (best < 0 || p < best) {
				best = p
			}
		}
		if best >= 0 {
			hits = append(hits, hit{id, best})
		}
	}
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && (hits[j].pos < hits[j-1].pos || (hits[j].pos == hits[j-1].pos && hits[j].id.String() < hits[j-1].id.String())); j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	out := make([]uuid.UUID, len(hits))
	for i, h := range hits {
		out[i] = h.id
	}
	return out
}

// indexWord finds needle in haystack at a word boundary (so "Lin" does
// not match inside "Linger"); for scripts without spaces any match counts.
func indexWord(haystack, needle string) int {
	if needle == "" {
		return -1
	}
	from := 0
	for {
		i := strings.Index(haystack[from:], needle)
		if i < 0 {
			return -1
		}
		i += from
		before := i == 0 || !isWordRune(lastRune(haystack[:i]))
		end := i + len(needle)
		after := end >= len(haystack) || !isWordRune(firstRune(haystack[end:]))
		if (before && after) || !hasSpaces(needle, haystack) {
			return i
		}
		from = i + 1
	}
}

func hasSpaces(needle, haystack string) bool {
	return strings.ContainsFunc(haystack, unicode.IsSpace) || strings.ContainsFunc(needle, unicode.IsSpace)
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func lastRune(s string) rune {
	r := []rune(s)
	if len(r) == 0 {
		return ' '
	}
	return r[len(r)-1]
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return ' '
}

// Draft is one scene produced by a split, before it is stored.
type Draft struct {
	ParagraphIDs []string
	Narration    string
	Segments     []Segment
	ImagePrompt  string
	CharacterIDs []uuid.UUID
	Tainted      bool
	EstimatedMs  int
}

// Cadence is the image-change cadence a split aims for, in seconds.
type Cadence struct{ MinS, MaxS int }

// DefaultCadence is 20–40 seconds per image.
var DefaultCadence = Cadence{MinS: 20, MaxS: 40}

// EstimateMs is the spoken duration estimate of text in lang.
func EstimateMs(text, lang string) int {
	return int(duration.Estimate(importer.WordCount(text), lang, "").Minutes * 60_000)
}

// TextHash is the narration hash re-split compares.
func TextHash(narration string) string {
	sum := sha256.Sum256([]byte(narration))
	return hex.EncodeToString(sum[:])
}

// SplitByParagraphs is the deterministic, non-LLM split: it groups whole
// paragraphs until a scene reaches the cadence minimum and never lets one
// grow past the maximum (a single paragraph longer than the maximum
// becomes a scene of its own). Quoted lines are attributed to the only
// character named in the paragraph, otherwise to the narrator.
func SplitByParagraphs(paragraphs []Paragraph, idx NameIndex, cadence Cadence, lang string) []Draft {
	minMs, maxMs := cadence.MinS*1000, cadence.MaxS*1000
	var groups [][]Paragraph
	var cur []Paragraph
	curMs := 0
	for _, p := range paragraphs {
		if strings.TrimSpace(p.Text) == "" {
			continue
		}
		ms := EstimateMs(p.Text, lang)
		if len(cur) > 0 && (curMs >= minMs || curMs+ms > maxMs) {
			groups = append(groups, cur)
			cur, curMs = nil, 0
		}
		cur = append(cur, p)
		curMs += ms
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	out := make([]Draft, 0, len(groups))
	for _, g := range groups {
		out = append(out, buildDraft(g, idx, lang, func(p Paragraph, _ int) (*uuid.UUID, string) {
			mentioned := idx.Mentioned(p.Text)
			if len(mentioned) == 1 {
				return &mentioned[0], ""
			}
			return nil, ""
		}, ""))
	}
	return out
}

// buildDraft assembles a scene from its paragraphs. speakerFor picks the
// speaker of the n-th quoted span of a paragraph: a character id, or nil
// for the narrator together with an unrecognised name to flag (if any).
func buildDraft(ps []Paragraph, idx NameIndex, lang string, speakerFor func(p Paragraph, quoteN int) (*uuid.UUID, string), imagePrompt string) Draft {
	d := Draft{}
	var texts []string
	seen := map[uuid.UUID]bool{}
	for _, p := range ps {
		d.ParagraphIDs = append(d.ParagraphIDs, p.ID)
		texts = append(texts, strings.TrimSpace(p.Text))
		d.Tainted = d.Tainted || p.Tainted
		quoteN := 0
		for _, span := range SplitSpans(p.Text) {
			var speaker *uuid.UUID
			var unrecognised string
			if span.Quote {
				speaker, unrecognised = speakerFor(p, quoteN)
				quoteN++
			}
			d.Segments = appendSegment(d.Segments, Segment{SpeakerCharacterID: speaker, Text: span.Text, UnrecognisedName: unrecognised})
		}
		for _, id := range idx.Mentioned(p.Text) {
			if !seen[id] {
				seen[id] = true
				d.CharacterIDs = append(d.CharacterIDs, id)
			}
		}
	}
	d.Narration = strings.Join(texts, "\n\n")
	d.EstimatedMs = EstimateMs(d.Narration, lang)
	d.ImagePrompt = imagePrompt
	if d.ImagePrompt == "" {
		d.ImagePrompt = firstSentence(d.Narration, 300)
	}
	for _, s := range d.Segments {
		if s.SpeakerCharacterID != nil && !seen[*s.SpeakerCharacterID] {
			seen[*s.SpeakerCharacterID] = true
			d.CharacterIDs = append(d.CharacterIDs, *s.SpeakerCharacterID)
		}
	}
	return d
}

// appendSegment merges consecutive spans of the same speaker (and the
// same unrecognised name) into one segment, so one voice call covers them.
func appendSegment(segs []Segment, s Segment) []Segment {
	if n := len(segs); n > 0 {
		last := &segs[n-1]
		if sameSpeaker(last.SpeakerCharacterID, s.SpeakerCharacterID) && last.UnrecognisedName == s.UnrecognisedName {
			last.Text += " " + s.Text
			return segs
		}
	}
	return append(segs, s)
}

func sameSpeaker(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// firstSentence is the paragraph mode's image prompt: the scene's opening
// sentence, capped at maxBytes on a rune boundary.
func firstSentence(text string, maxBytes int) string {
	text = strings.TrimSpace(text)
	end := strings.IndexFunc(text, func(r rune) bool { return r == '.' || r == '!' || r == '?' || r == '。' || r == '\n' })
	if end >= 0 {
		text = text[:end+1]
	}
	if len(text) > maxBytes {
		cut := maxBytes
		for cut > 0 && (text[cut]&0xC0) == 0x80 {
			cut--
		}
		text = text[:cut]
	}
	return strings.TrimSpace(text)
}

// SegmentsText joins segment texts: the narration a set of segments voices.
func SegmentsText(segs []Segment) string {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		parts = append(parts, s.Text)
	}
	return strings.Join(parts, " ")
}
