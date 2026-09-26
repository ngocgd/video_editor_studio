package importer

import (
	"regexp"
	"strings"
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
// 1-indexed chapters. PresetAuto tries every non-auto preset and returns
// the result with the most matches (a tie keeps Chinese > English >
// Vietnamese, an arbitrary but deterministic order); a text with no
// preset producing at least one match returns a single chapter spanning
// the whole text under an empty title.
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
	runes := []rune(text)
	return []Chapter{{Index: 1, Title: "", CharStart: 0, CharEnd: len(runes), WordCount: WordCount(text)}}
}

func splitWithPattern(text string, pattern *regexp.Regexp) []Chapter {
	locs := pattern.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}

	runes := []rune(text)
	byteToRune := byteOffsetToRuneOffset(text)

	var chapters []Chapter
	for i, loc := range locs {
		startRune := byteToRune[loc[0]]
		endRune := len(runes)
		if i+1 < len(locs) {
			endRune = byteToRune[locs[i+1][0]]
		}

		title := strings.TrimSpace(text[loc[0]:loc[1]])
		chapters = append(chapters, Chapter{
			Index:     i + 1,
			Title:     title,
			CharStart: startRune,
			CharEnd:   endRune,
			WordCount: WordCount(string(runes[startRune:endRune])),
		})
	}
	return chapters
}

// byteOffsetToRuneOffset builds a lookup from every valid rune-start byte
// offset in s to its rune index, so regex byte offsets (Go regexp always
// reports byte offsets) can be converted to rune offsets for CJK-safe
// slicing.
func byteOffsetToRuneOffset(s string) map[int]int {
	m := make(map[int]int, len(s))
	runeIdx := 0
	for byteIdx := range s {
		m[byteIdx] = runeIdx
		runeIdx++
	}
	m[len(s)] = runeIdx
	return m
}
