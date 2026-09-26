package importer

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// SplitPreset names the regex preset used to find chapter boundaries,
// stored verbatim in imports.split_preset.
type SplitPreset string

const (
	PresetChinese SplitPreset = "zh"
	PresetEnglish SplitPreset = "en"
	PresetViet    SplitPreset = "vi"
	PresetAuto    SplitPreset = "auto"
)

// chapterPatterns maps each non-auto preset to its compiled boundary
// regex. Patterns match the whole heading line so Title can report it.
var chapterPatterns = map[SplitPreset]*regexp.Regexp{
	PresetChinese: regexp.MustCompile(`(?m)^第.{1,9}章.*$`),
	PresetEnglish: regexp.MustCompile(`(?m)^\s*Chapter\s+\d+.*$`),
	PresetViet:    regexp.MustCompile(`(?m)^\s*Chương\s+\d+.*$`),
}

// Chapter is one detected chapter span within the decoded manuscript
// text. CharStart/CharEnd are rune offsets into the source text (used
// internally by commit to slice the chapter's own content); Title and
// WordCount are what the preview endpoint exposes.
type Chapter struct {
	Index     int
	Title     string
	CharStart int
	CharEnd   int
	WordCount int
}

// Text returns the chapter's own slice of the source text (heading
// included) given the same text Split was called with.
func (c Chapter) Text(source []rune) string {
	if c.CharStart < 0 || c.CharEnd > len(source) || c.CharStart > c.CharEnd {
		return ""
	}
	return string(source[c.CharStart:c.CharEnd])
}

// Split finds chapter boundaries in text using preset, returning ordered,
// 1-indexed chapters; non-blank text before the first heading becomes a
// leading chapter titled PrefaceTitle. PresetAuto tries every non-auto
// preset and returns the result with the most matches (a tie keeps
// Chinese > English > Vietnamese, an arbitrary but deterministic order);
// a text with no preset producing at least one match returns a single
// chapter spanning the whole text under an empty title.
func Split(text string, preset SplitPreset) (chapters []Chapter, usedPreset SplitPreset) {
	if preset == PresetAuto || preset == "" {
		bestPreset := SplitPreset("")
		bestCount := 0
		for _, p := range []SplitPreset{PresetChinese, PresetEnglish, PresetViet} {
			if n := len(chapterPatterns[p].FindAllStringIndex(text, -1)); n > bestCount {
				bestCount = n
				bestPreset = p
			}
		}
		if bestPreset == "" {
			return wholeTextChapter(text), PresetAuto
		}
		return splitWithPattern(text, chapterPatterns[bestPreset]), bestPreset
	}

	pattern, ok := chapterPatterns[preset]
	if !ok {
		return wholeTextChapter(text), preset
	}
	chs := splitWithPattern(text, pattern)
	if len(chs) == 0 {
		return wholeTextChapter(text), preset
	}
	return chs, preset
}

func wholeTextChapter(text string) []Chapter {
	return []Chapter{{Index: 1, Title: "", CharStart: 0, CharEnd: utf8.RuneCountInString(text), WordCount: WordCount(text)}}
}

// PrefaceTitle is the title given to the text that precedes the first
// chapter heading, so a foreword or synopsis is imported rather than
// silently dropped.
const PrefaceTitle = "Preface"

func splitWithPattern(text string, pattern *regexp.Regexp) []Chapter {
	locs := pattern.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}

	// Chapter boundaries as byte offsets: an optional preface span first,
	// then one span per heading, each ending where the next begins.
	type span struct {
		start, end int
		title      string
	}
	spans := make([]span, 0, len(locs)+1)
	if strings.TrimSpace(text[:locs[0][0]]) != "" {
		spans = append(spans, span{start: 0, end: locs[0][0], title: PrefaceTitle})
	}
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		spans = append(spans, span{start: loc[0], end: end, title: strings.TrimSpace(text[loc[0]:loc[1]])})
	}

	// Spans are contiguous and ascending (only whitespace before the first
	// heading can be skipped), so one running rune counter converts every
	// byte offset without materialising the whole text as runes or a
	// per-rune lookup table.
	chapters := make([]Chapter, 0, len(spans))
	runePos := utf8.RuneCountInString(text[:spans[0].start])
	for i, sp := range spans {
		chapterText := text[sp.start:sp.end]
		startRune := runePos
		runePos += utf8.RuneCountInString(chapterText)
		chapters = append(chapters, Chapter{
			Index:     i + 1,
			Title:     sp.title,
			CharStart: startRune,
			CharEnd:   runePos,
			WordCount: WordCount(chapterText),
		})
	}
	return chapters
}
