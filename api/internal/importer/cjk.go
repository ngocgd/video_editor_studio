package importer

import "unicode"

// cjkRatioThreshold is the minimum share of CJK-vs-total letters a text
// needs for IsMostlyCJK to call it Chinese-language prose. Below this, a
// mostly-Latin text with a few CJK punctuation marks or loanwords is not
// misclassified; at or above it, prose written with essentially no Latin
// letters (occasional romanized names aside) is.
const cjkRatioThreshold = 0.3

// isCJKRune reports whether r is a CJK (Han) ideograph.
func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

// cjkRatio returns the fraction of letter runes in s that are CJK
// ideographs, 0 when s has no letters at all.
func cjkRatio(s string) float64 {
	letters, cjk := 0, 0
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if isCJKRune(r) {
			cjk++
		}
	}
	if letters == 0 {
		return 0
	}
	return float64(cjk) / float64(letters)
}

// IsMostlyCJK reports whether s is Chinese-language prose: CJK ideographs
// make up at least cjkRatioThreshold of its letters. Used by the import
// commit path to decide whether a decoded chapter's source draft is stored
// under lang="zh" rather than "en".
func IsMostlyCJK(s string) bool {
	return cjkRatio(s) >= cjkRatioThreshold
}

// WordCount counts words in s: a whitespace-separated run of non-CJK
// letters is one word, matching plain English/Vietnamese prose; CJK text
// has no spaces between words, so each CJK rune is counted as its own word
// instead, a coarse but stable per-character proxy. This is the single
// heuristic used everywhere a draft or chapter's word_count is computed, so
// an imported chapter's preview count and its resulting draft's word count
// never disagree.
func WordCount(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		switch {
		case isCJKRune(r):
			count++
			inWord = false
		case unicode.IsSpace(r):
			inWord = false
		case !inWord:
			count++
			inWord = true
		}
	}
	return count
}
