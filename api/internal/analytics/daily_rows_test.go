package analytics

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateTextCountsCharactersNotBytes(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exact", 5, "exact"},
		{"abcdef", 3, "abc"},
		{"Lâm Mặc", 5, "Lâm M"},
		{"", 3, ""},
	}
	for _, c := range cases {
		if got := truncateText(c.in, c.max); got != c.want {
			t.Errorf("truncateText(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
	long := strings.Repeat("ặ", maxSyncErrorLen+50)
	got := truncateText(long, maxSyncErrorLen)
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != maxSyncErrorLen {
		t.Errorf("multibyte text cut to %d runes, valid=%v", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
}
