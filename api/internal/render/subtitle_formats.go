package render

import (
	"fmt"
	"strconv"
	"strings"
)

// SRT renders cues as a SubRip file. Text is cleaned of control
// characters and blank lines (a blank line would end the cue early) and
// of "-->" (which would read as a timing line).
func SRT(cues []Cue) string {
	var b strings.Builder
	n := 0
	for _, c := range cues {
		lines := cleanText(strings.ReplaceAll(c.Text, "-->", "->"))
		if len(lines) == 0 || c.EndMs <= c.StartMs {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, srtTime(c.StartMs), srtTime(c.EndMs), strings.Join(lines, "\n"))
	}
	return b.String()
}

func srtTime(ms int64) string {
	ms = max(ms, 0)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// assAlignment maps a subtitle position to the ASS numpad alignment.
var assAlignment = map[string]int{"bottom": 2, "middle": 5, "top": 8}

// ASS renders cues as an Advanced SubStation file sized for a w×h frame
// with the settings' style: white text, a soft drop shadow, no outline.
// Line wrapping is done by the cue builder, so libass's own wrapping is
// off (WrapStyle 2).
func ASS(cues []Cue, style SubtitleStyle, w, h int) string {
	var b strings.Builder
	b.WriteString("[Script Info]\nScriptType: v4.00+\n")
	fmt.Fprintf(&b, "PlayResX: %d\nPlayResY: %d\nWrapStyle: 2\nScaledBorderAndShadow: yes\n\n", w, h)
	b.WriteString("[V4+ Styles]\n")
	b.WriteString("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, " +
		"Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, " +
		"Alignment, MarginL, MarginR, MarginV, Encoding\n")
	align, ok := assAlignment[style.Position]
	if !ok {
		align = 2
	}
	margin := h * 6 / 100
	if !fontPattern.MatchString(style.Font) {
		// The name lands in a comma-separated header line.
		style.Font = DefaultSettings().SubtitleStyle.Font
	}
	fmt.Fprintf(&b, "Style: Default,%s,%d,&H00FFFFFF,&H00FFFFFF,&H00000000,&H96000000,0,0,0,0,100,100,0,0,1,0,%d,%d,%d,%d,%d,1\n\n",
		style.Font, style.SizePx, style.ShadowPx, align, margin, margin, margin)
	b.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	for _, c := range cues {
		lines := cleanText(c.Text)
		if len(lines) == 0 || c.EndMs <= c.StartMs {
			continue
		}
		escaped := make([]string, len(lines))
		for i, l := range lines {
			escaped[i] = EscapeASS(l)
		}
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTime(c.StartMs), assTime(c.EndMs), strings.Join(escaped, `\N`))
	}
	return b.String()
}

// EscapeASS makes one line of cue text inert for libass. A brace would
// open an override block ({\pos...}, {\fn...}) and a backslash starts
// \N, \h and friends, so both are replaced by their full-width look-
// alikes rather than escaped (libass escape support varies by version).
func EscapeASS(s string) string {
	return strings.NewReplacer(`\`, "＼", "{", "｛", "}", "｝").Replace(s)
}

// assTime formats h:mm:ss.cc; ASS has centisecond resolution, so times
// are rounded to the nearest centisecond.
func assTime(ms int64) string {
	cs := (max(ms, 0) + 5) / 10
	return strconv.FormatInt(cs/360000, 10) + fmt.Sprintf(":%02d:%02d.%02d", cs/6000%60, cs/100%60, cs%100)
}
