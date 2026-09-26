// Package render turns a frozen render manifest into an episode video:
// a frame-exact timeline of scene bodies and transitions, motion
// filtergraphs, subtitles, a loudness-normalised narration track and
// content hashes that let unchanged segments be reused across renders.
// The pure parts (timeline, filtergraphs, hashes, subtitles) live here;
// the steps that run them sit next to them.
package render

import (
	"fmt"
	"regexp"
)

// Motion presets, named as scenes.motion_preset stores them.
const (
	MotionStatic   = "static"
	MotionKenBurns = "ken_burns"
	MotionParallax = "parallax"
)

// Subtitle delivery modes.
const (
	SubtitlesBurn = "burn"
	SubtitlesSRT  = "srt"
	SubtitlesBoth = "both"
)

// Encoder choices of the settings; EncoderAuto resolves to the encoder
// the render worker's probe found.
const (
	EncoderAuto  = "auto"
	EncoderNVENC = "h264_nvenc"
	EncoderX264  = "libx264"
)

// SubtitleStyle is the burned-in subtitle look.
type SubtitleStyle struct {
	Font     string `json:"font"`
	SizePx   int    `json:"sizePx"`
	Position string `json:"position"`
	ShadowPx int    `json:"shadowPx"`
}

// Settings is the resolved render settings snapshot frozen into a
// manifest. Encoder is always a concrete encoder there, never auto,
// because segments from different encoders cannot be joined by copy.
type Settings struct {
	Width         int           `json:"width"`
	Height        int           `json:"height"`
	FPS           int           `json:"fps"`
	Encoder       string        `json:"encoder"`
	Subtitles     string        `json:"subtitles"`
	SubtitleStyle SubtitleStyle `json:"subtitleStyle"`
	DefaultMotion string        `json:"defaultMotion"`
	CrossfadeMs   int           `json:"crossfadeMs"`
	// Loudness target in tenths: -140 is -14.0 LUFS, -10 is -1.0 dBTP.
	LoudnessLUFSx10 int `json:"loudnessLufsX10"`
	TruePeakDBTPx10 int `json:"truePeakDbtpX10"`
}

// DefaultSettings are the settings of an episode nobody has configured.
func DefaultSettings() Settings {
	return Settings{
		Width: 1920, Height: 1080, FPS: 30, Encoder: EncoderAuto, Subtitles: SubtitlesBoth,
		SubtitleStyle: SubtitleStyle{Font: "Literata", SizePx: 42, Position: "bottom", ShadowPx: 2},
		DefaultMotion: MotionKenBurns, CrossfadeMs: 600, LoudnessLUFSx10: -140, TruePeakDBTPx10: -10,
	}
}

var fontPattern = regexp.MustCompile(`^[A-Za-z0-9 ]{1,64}$`)

// Validate checks the ranges the database enforces plus the style fields
// that end up inside the ASS header.
func (s Settings) Validate() error {
	switch {
	case s.Width < 320 || s.Width > 3840 || s.Width%2 != 0, s.Height < 180 || s.Height > 2160 || s.Height%2 != 0:
		return fmt.Errorf("render: frame size %dx%d is out of range", s.Width, s.Height)
	case s.FPS != 24 && s.FPS != 25 && s.FPS != 30 && s.FPS != 60:
		return fmt.Errorf("render: fps %d is not supported", s.FPS)
	case s.Encoder != EncoderAuto && s.Encoder != EncoderNVENC && s.Encoder != EncoderX264:
		return fmt.Errorf("render: encoder %q is not supported", s.Encoder)
	case s.Subtitles != SubtitlesBurn && s.Subtitles != SubtitlesSRT && s.Subtitles != SubtitlesBoth:
		return fmt.Errorf("render: subtitle mode %q is not supported", s.Subtitles)
	case s.DefaultMotion != MotionStatic && s.DefaultMotion != MotionKenBurns && s.DefaultMotion != MotionParallax:
		return fmt.Errorf("render: motion %q is not supported", s.DefaultMotion)
	case s.CrossfadeMs < 0 || s.CrossfadeMs > 3000:
		return fmt.Errorf("render: crossfade %dms is out of range", s.CrossfadeMs)
	case s.LoudnessLUFSx10 < -300 || s.LoudnessLUFSx10 > -50 || s.TruePeakDBTPx10 < -90 || s.TruePeakDBTPx10 > 0:
		return fmt.Errorf("render: loudness target is out of range")
	case !fontPattern.MatchString(s.SubtitleStyle.Font):
		return fmt.Errorf("render: subtitle font name is not allowed")
	case s.SubtitleStyle.SizePx < 12 || s.SubtitleStyle.SizePx > 160 || s.SubtitleStyle.ShadowPx < 0 || s.SubtitleStyle.ShadowPx > 10:
		return fmt.Errorf("render: subtitle size or shadow is out of range")
	case s.SubtitleStyle.Position != "bottom" && s.SubtitleStyle.Position != "top" && s.SubtitleStyle.Position != "middle":
		return fmt.Errorf("render: subtitle position %q is not supported", s.SubtitleStyle.Position)
	}
	return nil
}

// Burn reports whether subtitles are burned into the picture.
func (s Settings) Burn() bool { return s.Subtitles == SubtitlesBurn || s.Subtitles == SubtitlesBoth }

// SoftSubtitles reports whether a subtitle stream is muxed into the MP4.
func (s Settings) SoftSubtitles() bool {
	return s.Subtitles == SubtitlesSRT || s.Subtitles == SubtitlesBoth
}

// CrossfadeFrames is the crossfade length in whole frames.
func (s Settings) CrossfadeFrames() int { return FramesForMs(int64(s.CrossfadeMs), s.FPS) }
