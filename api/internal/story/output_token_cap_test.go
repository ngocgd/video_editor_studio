package story

import (
	"strings"
	"testing"
)

func TestEpisodeOutputTokenCapGrowsWithTheTarget(t *testing.T) {
	cases := []struct {
		name   string
		target string
		want   int
	}{
		{"empty target keeps the floor", "", minEpisodeOutputTokens},
		{"a short selection keeps the floor", strings.Repeat("字", 100), minEpisodeOutputTokens},
		{"a long Chinese chapter gets room for its translation", strings.Repeat("字", 6000), 6000*3/2 + outputTokenSlack},
		{"runes, not bytes, drive the estimate", strings.Repeat("é", 4000), 4000*3/2 + outputTokenSlack},
		{"a huge chapter stops at the ceiling", strings.Repeat("字", 50000), maxEpisodeOutputTokens},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := episodeOutputTokenCap(tc.target); got != tc.want {
				t.Fatalf("episodeOutputTokenCap(%d runes) = %d, want %d", len([]rune(tc.target)), got, tc.want)
			}
		})
	}
}

func TestEpisodeOutputTokenCapExceedsTheOldFixedCapForAChapter(t *testing.T) {
	// A 5,000-character chapter needs more than the old fixed 4,000 tokens
	// to come back whole.
	if got := episodeOutputTokenCap(strings.Repeat("字", 5000)); got <= 4000 {
		t.Fatalf("cap for a 5,000-character chapter = %d, want more than 4000", got)
	}
}
