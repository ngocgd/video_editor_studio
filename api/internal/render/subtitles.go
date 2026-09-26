package render

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AlignDoc is the alignment document the align step stores per voice
// take: cues with times in seconds, and word times for word-level
// (English) alignment.
type AlignDoc struct {
	Language    string         `json:"language"`
	Granularity string         `json:"granularity"`
	Segments    []AlignSegment `json:"segments"`
}

// AlignSegment is one aligned sentence or phrase.
type AlignSegment struct {
	Text  string      `json:"text"`
	Start float64     `json:"start"`
	End   float64     `json:"end"`
	Words []AlignWord `json:"words,omitempty"`
}

// AlignWord is one aligned word.
type AlignWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// ParseAlignDoc decodes an alignment document.
func ParseAlignDoc(data []byte) (AlignDoc, error) {
	var d AlignDoc
	if err := json.Unmarshal(data, &d); err != nil {
		return AlignDoc{}, fmt.Errorf("render: unreadable alignment: %w", err)
	}
	return d, nil
}

// Cue is one subtitle on the episode (or segment) clock. Text holds at
// most MaxCueLines lines separated by "\n".
type Cue struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Text    string `json:"text"`
}

// Subtitle layout limits.
const (
	MaxLineChars = 42
	MaxCueLines  = 2
	// wordGapBreakMs starts a new cue at a pause this long, so a cue
	// never hangs on screen across silence.
	wordGapBreakMs = 700
)

// SceneCues builds the cues of one scene, shifted to the scene's start
// on the episode clock and clipped to the scene. English word-level
// alignment is regrouped into cues of up to two 42-character lines;
// segment-level alignment (Vietnamese) keeps one cue per aligned
// segment, wrapped the same way and split in time by text length when
// it needs more than two lines.
func SceneCues(doc AlignDoc, sceneStartMs, sceneDurMs int64) []Cue {
	var out []Cue
	for _, seg := range doc.Segments {
		var cues []Cue
		if doc.Granularity == "word" && len(seg.Words) > 0 {
			cues = wordCues(seg.Words)
		} else {
			cues = segmentCues(seg.Text, toMs(seg.Start), toMs(seg.End))
		}
		for _, c := range cues {
			c.StartMs, c.EndMs = clamp(c.StartMs, 0, sceneDurMs), clamp(c.EndMs, 0, sceneDurMs)
			if c.EndMs <= c.StartMs || strings.TrimSpace(c.Text) == "" {
				continue
			}
			c.StartMs += sceneStartMs
			c.EndMs += sceneStartMs
			out = append(out, c)
		}
	}
	return out
}

func wordCues(words []AlignWord) []Cue {
	var out []Cue
	var lines []string
	var line string
	var start, end int64
	flush := func() {
		if line != "" {
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			out = append(out, Cue{StartMs: start, EndMs: end, Text: strings.Join(lines, "\n")})
		}
		lines, line = nil, ""
	}
	for _, w := range words {
		text := strings.TrimSpace(w.Word)
		if text == "" {
			continue
		}
		ws, we := toMs(w.Start), toMs(w.End)
		if (line != "" || len(lines) > 0) && ws-end >= wordGapBreakMs {
			flush()
		}
		if line == "" && len(lines) == 0 {
			start = ws
		}
		switch {
		case line == "":
			line = text
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(text) <= MaxLineChars:
			line += " " + text
		default:
			lines = append(lines, line)
			line = text
			if len(lines) == MaxCueLines {
				out = append(out, Cue{StartMs: start, EndMs: end, Text: strings.Join(lines, "\n")})
				lines, start = nil, ws
			}
		}
		end = max(we, ws)
	}
	flush()
	return out
}

func segmentCues(text string, startMs, endMs int64) []Cue {
	lines := wrap(text, MaxLineChars)
	if len(lines) == 0 || endMs <= startMs {
		return nil
	}
	total := 0
	for _, l := range lines {
		total += utf8.RuneCountInString(l)
	}
	var out []Cue
	done := 0
	for i := 0; i < len(lines); i += MaxCueLines {
		group := lines[i:min(i+MaxCueLines, len(lines))]
		n := 0
		for _, l := range group {
			n += utf8.RuneCountInString(l)
		}
		s := startMs + (endMs-startMs)*int64(done)/int64(total)
		done += n
		e := startMs + (endMs-startMs)*int64(done)/int64(total)
		out = append(out, Cue{StartMs: s, EndMs: e, Text: strings.Join(group, "\n")})
	}
	return out
}

// wrap breaks text into lines of at most width runes at spaces; a single
// longer word gets a line of its own.
func wrap(text string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		switch {
		case line == "":
			line = w
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// CuesInRange returns the cues overlapping [startMs, endMs), clipped and
// shifted so startMs is 0: the cues burned into one segment.
func CuesInRange(cues []Cue, startMs, endMs int64) []Cue {
	var out []Cue
	for _, c := range cues {
		s, e := max(c.StartMs, startMs), min(c.EndMs, endMs)
		if e > s {
			out = append(out, Cue{StartMs: s - startMs, EndMs: e - startMs, Text: c.Text})
		}
	}
	return out
}

func toMs(sec float64) int64 {
	if math.IsNaN(sec) || sec < 0 {
		return 0
	}
	return int64(math.Round(sec * 1000))
}

func clamp(v, lo, hi int64) int64 { return max(lo, min(v, hi)) }

// cleanText drops control characters and collapses whitespace inside a
// line, keeping the line breaks the cue builder put in.
func cleanText(text string) []string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, l)
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// SegmentCues returns the cues burned into seg, on the segment's own
// clock. sceneCues(i) must return scene i's cues on the scene's own clock
// (SceneCues with a start of 0), so the result does not depend on where
// the scene sits in the episode, just as SegmentHash does not.
func SegmentCues(t Timeline, seg Segment, sceneCues func(scene int) []Cue) []Cue {
	fps := t.FPS
	if seg.Kind == SegmentBody {
		return CuesInRange(sceneCues(seg.Scene), FrameMs(seg.LocalStart, fps), FrameMs(seg.LocalStart+seg.Frames, fps))
	}
	from := t.Scenes[seg.Scene]
	out := CuesInRange(sceneCues(seg.Scene), FrameMs(seg.LocalStart, fps), FrameMs(from.Frames, fps))
	offset := FrameMs(from.Frames-seg.LocalStart, fps)
	for _, c := range CuesInRange(sceneCues(seg.Scene+1), 0, FrameMs(-seg.NextLocalStart, fps)) {
		out = append(out, Cue{StartMs: c.StartMs + offset, EndMs: c.EndMs + offset, Text: c.Text})
	}
	return out
}
