package render

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func words(text string, start, step float64) []AlignWord {
	var out []AlignWord
	for i, w := range strings.Fields(text) {
		s := start + float64(i)*step
		out = append(out, AlignWord{Word: w, Start: s, End: s + step*0.8})
	}
	return out
}

func TestEnglishWordsGroupIntoTwoLineCues(t *testing.T) {
	text := "The old lighthouse keeper climbed the spiral stairs every night before the storm rolled in from the grey northern sea"
	doc := AlignDoc{Granularity: "word", Segments: []AlignSegment{{Text: text, Start: 0, End: 10, Words: words(text, 0.5, 0.3)}}}
	cues := SceneCues(doc, 10000, 60000)
	if len(cues) < 2 {
		t.Fatalf("cues = %+v", cues)
	}
	var rebuilt []string
	for i, c := range cues {
		lines := strings.Split(c.Text, "\n")
		if len(lines) > MaxCueLines {
			t.Fatalf("cue %d has %d lines", i, len(lines))
		}
		for _, l := range lines {
			if utf8.RuneCountInString(l) > MaxLineChars {
				t.Fatalf("line %q is longer than %d", l, MaxLineChars)
			}
			rebuilt = append(rebuilt, l)
		}
		if c.StartMs < 10000 || c.EndMs <= c.StartMs || (i > 0 && c.StartMs < cues[i-1].EndMs) {
			t.Fatalf("cue %d times %d-%d are not ordered on the episode clock", i, c.StartMs, c.EndMs)
		}
	}
	if strings.Join(rebuilt, " ") != text {
		t.Fatalf("cues lost words: %q", strings.Join(rebuilt, " "))
	}
	if cues[0].StartMs != 10500 {
		t.Fatalf("first cue starts at %d, want the first word at 10500", cues[0].StartMs)
	}
}

func TestEnglishCueBreaksAtAPause(t *testing.T) {
	ws := []AlignWord{{Word: "Hello", Start: 0, End: 0.4}, {Word: "there.", Start: 0.5, End: 0.9}, {Word: "Later", Start: 3, End: 3.4}}
	cues := SceneCues(AlignDoc{Granularity: "word", Segments: []AlignSegment{{Words: ws}}}, 0, 10000)
	if len(cues) != 2 || cues[0].Text != "Hello there." || cues[1].Text != "Later" || cues[0].EndMs != 900 {
		t.Fatalf("cues = %+v", cues)
	}
}

func TestVietnameseSegmentsSplitByLength(t *testing.T) {
	text := "Ngọn hải đăng cũ đứng lặng lẽ trên vách đá, nhìn ra biển xám mỗi đêm khi cơn bão kéo về từ phương bắc xa xôi"
	doc := AlignDoc{Granularity: "segment", Segments: []AlignSegment{{Text: text, Start: 1, End: 9}}}
	cues := SceneCues(doc, 0, 8500)
	if len(cues) != 2 {
		t.Fatalf("cues = %+v", cues)
	}
	if cues[0].StartMs != 1000 || cues[1].EndMs != 8500 || cues[0].EndMs != cues[1].StartMs {
		t.Fatalf("cue times = %+v (the last one is clipped to the scene)", cues)
	}
}

func TestCuesInRangeClipsAndShifts(t *testing.T) {
	cues := []Cue{{StartMs: 0, EndMs: 1000, Text: "a"}, {StartMs: 1500, EndMs: 3000, Text: "b"}, {StartMs: 4000, EndMs: 5000, Text: "c"}}
	got := CuesInRange(cues, 2000, 4000)
	if len(got) != 1 || got[0] != (Cue{StartMs: 0, EndMs: 1000, Text: "b"}) {
		t.Fatalf("range = %+v", got)
	}
}

func TestSRTFormat(t *testing.T) {
	got := SRT([]Cue{{StartMs: 1234, EndMs: 3723005, Text: "one\n\ntwo --> three\x07"}, {StartMs: 5, EndMs: 5, Text: "empty"}})
	want := "1\n00:00:01,234 --> 01:02:03,005\none\ntwo -> three\n\n"
	if got != want {
		t.Fatalf("srt =\n%q\nwant\n%q", got, want)
	}
}

func TestASSEscapesOverrideTags(t *testing.T) {
	cue := Cue{StartMs: 0, EndMs: 1500, Text: `{\pos(0,0)}pwned \N x` + "\nline two"}
	got := ASS([]Cue{cue}, DefaultSettings().SubtitleStyle, 1920, 1080)
	dialogue := got[strings.Index(got, "Dialogue:"):]
	if strings.ContainsAny(strings.TrimPrefix(dialogue, "Dialogue: 0,0:00:00.00,0:00:01.50,Default,,0,0,0,,"), "{}") {
		t.Fatalf("braces survived: %s", dialogue)
	}
	if !strings.Contains(dialogue, `｛＼pos(0,0)｝pwned ＼N x\Nline two`) {
		t.Fatalf("dialogue = %s", dialogue)
	}
	if !strings.Contains(got, "Style: Default,Literata,42,") || !strings.Contains(got, ",1,0,2,2,64,64,64,1\n") {
		t.Fatalf("style line wrong:\n%s", got)
	}
}

func TestASSFallsBackToTheDefaultFont(t *testing.T) {
	style := SubtitleStyle{Font: "Evil,Font", SizePx: 42, Position: "top", ShadowPx: 2}
	got := ASS(nil, style, 1280, 720)
	if !strings.Contains(got, "Style: Default,Literata,42,") || !strings.Contains(got, ",2,8,43,43,43,1\n") {
		t.Fatalf("style = %s", got)
	}
}

func TestParseAlignDoc(t *testing.T) {
	doc, err := ParseAlignDoc([]byte(`{"language":"en","granularity":"word","segments":[{"text":"Hi.","start":0.1,"end":0.5,"words":[{"word":"Hi.","start":0.1,"end":0.5}]}]}`))
	if err != nil || len(doc.Segments) != 1 || len(doc.Segments[0].Words) != 1 {
		t.Fatalf("doc = %+v, %v", doc, err)
	}
	if _, err := ParseAlignDoc([]byte("nope")); err == nil {
		t.Fatal("garbage must not parse")
	}
}
