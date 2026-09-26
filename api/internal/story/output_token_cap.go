package story

import "unicode/utf8"

const (
	// minEpisodeOutputTokens is the reply cap for short targets (a beat
	// summary, a selected paragraph): room for a few paragraphs of prose.
	minEpisodeOutputTokens = 4000
	// maxEpisodeOutputTokens is the largest reply cap requested: within
	// the output limit of the hosted models the LLM settings offer, and
	// enough for a long chapter translated in one go.
	maxEpisodeOutputTokens = 16384
	// outputTokenSlack covers the reply's own overhead (paragraph breaks,
	// a sentence that runs over) on top of the size estimate.
	outputTokenSlack = 512
)

// episodeOutputTokenCap sizes the reply cap from the text the action works
// on. Translate and rewrite reproduce the whole target, so a fixed cap cuts
// a long chapter off mid-sentence. One and a half tokens per rune is an
// upper estimate for both Han text (about one token per character, and an
// English or Vietnamese rendering runs longer) and Latin text (about a
// quarter token per character), so the cap only ever errs on the generous
// side, then stays within [minEpisodeOutputTokens, maxEpisodeOutputTokens].
func episodeOutputTokenCap(targetText string) int {
	estimate := utf8.RuneCountInString(targetText)*3/2 + outputTokenSlack
	return min(max(estimate, minEpisodeOutputTokens), maxEpisodeOutputTokens)
}
