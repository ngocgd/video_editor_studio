package importer

import "testing"

func TestIsMostlyCJK(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"chinese prose", "林默在黄昏时分登上了禁峰。", true},
		{"chinese with a romanized name", "Lin Mo 在黄昏时分登上了禁峰，风很大。", true},
		{"english prose", "Lin Mo climbed the forbidden peak at dusk.", false},
		{"english with one hanzi", "The sign read 峰 and nothing else in the whole valley.", false},
		{"no letters", "123 … !!!", false},
	}
	for _, c := range cases {
		if got := IsMostlyCJK(c.text); got != c.want {
			t.Errorf("%s: IsMostlyCJK = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWordCountCountsEachHanziAsAWord(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"Lin Mo climbed the peak.", 5},
		{"林默登山", 4},
		{"Lin Mo 登山 today", 5},
		{"", 0},
	}
	for _, c := range cases {
		if got := WordCount(c.text); got != c.want {
			t.Errorf("WordCount(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}
