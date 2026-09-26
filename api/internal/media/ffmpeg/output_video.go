package ffmpeg

import (
	"fmt"
	"regexp"
	"strconv"
)

// Video encoders a render may use, and stream copy.
const (
	CodecX264  = "libx264"
	CodecNVENC = "h264_nvenc"
	CodecCopy  = "copy"
)

var (
	x264Presets  = map[string]bool{"ultrafast": true, "superfast": true, "veryfast": true, "faster": true, "fast": true, "medium": true, "slow": true}
	nvencPresets = map[string]bool{"p1": true, "p2": true, "p3": true, "p4": true, "p5": true, "p6": true, "p7": true}
	pixelFormats = map[string]bool{"": true, "yuv420p": true}
	mapStream    = regexp.MustCompile(`^[0-9]+:[vas]$`)
	mapLabel     = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	langPattern  = regexp.MustCompile(`^[a-z]{3}$`)
)

// VideoEncode is the H.264 setup of a render segment or preview proxy.
// Every segment of one render uses the same values, so segments encoded
// at different times can be joined with the concat demuxer and -c copy.
type VideoEncode struct {
	// Preset is the encoder preset: ultrafast..slow for libx264, p1..p7
	// for h264_nvenc.
	Preset string
	// Quality is -crf for libx264 and -cq for h264_nvenc (0..51).
	Quality     int
	PixelFormat string
	// FrameRate is the output frame rate (-r).
	FrameRate int
	// GOP is the maximum keyframe interval in frames.
	GOP int
	// ClosedSegment makes the stream safe to cut and join: a keyframe at
	// the first frame, closed GOPs, no B-frames and no scene-cut
	// keyframes, so identical inputs give identical packet layouts.
	ClosedSegment bool
	// TimeScale fixes the MP4 video track timescale, so all segments
	// share one timebase.
	TimeScale int
}

func (v *VideoEncode) args(codec string) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	if codec != CodecX264 && codec != CodecNVENC {
		return nil, fmt.Errorf("%w: video settings need an H.264 encoder", ErrRefused)
	}
	if v.Quality < 0 || v.Quality > 51 || v.FrameRate < 0 || v.GOP < 0 || v.TimeScale < 0 || !pixelFormats[v.PixelFormat] {
		return nil, fmt.Errorf("%w: video settings are out of range", ErrRefused)
	}
	var args []string
	if v.Preset != "" {
		if (codec == CodecX264 && !x264Presets[v.Preset]) || (codec == CodecNVENC && !nvencPresets[v.Preset]) {
			return nil, fmt.Errorf("%w: preset %q is not allowed", ErrRefused, v.Preset)
		}
		args = append(args, "-preset", v.Preset)
	}
	args = append(args, "-profile:v", "high")
	if v.Quality > 0 {
		if codec == CodecX264 {
			args = append(args, "-crf", strconv.Itoa(v.Quality))
		} else {
			args = append(args, "-rc", "vbr", "-cq", strconv.Itoa(v.Quality), "-b:v", "0")
		}
	}
	if v.PixelFormat != "" {
		args = append(args, "-pix_fmt", v.PixelFormat)
	}
	if v.FrameRate > 0 {
		args = append(args, "-r", strconv.Itoa(v.FrameRate))
	}
	if v.GOP > 0 {
		args = append(args, "-g", strconv.Itoa(v.GOP))
		if codec == CodecX264 {
			args = append(args, "-keyint_min", strconv.Itoa(v.GOP))
		}
	}
	if v.ClosedSegment {
		args = append(args, "-bf", "0", "-force_key_frames", "expr:eq(n,0)")
		if codec == CodecX264 {
			args = append(args, "-sc_threshold", "0", "-flags", "+cgop")
		} else {
			args = append(args, "-forced-idr", "1")
		}
	}
	if v.TimeScale > 0 {
		args = append(args, "-video_track_timescale", strconv.Itoa(v.TimeScale))
	}
	return args, nil
}

// containerArgs covers the subtitle stream and MP4 muxer flags.
func (out Output) containerArgs() ([]string, error) {
	var args []string
	if out.SubtitleCodec != "" {
		args = append(args, "-c:s", out.SubtitleCodec)
	}
	if out.SubtitleLanguage != "" {
		if !langPattern.MatchString(out.SubtitleLanguage) {
			return nil, fmt.Errorf("%w: subtitle language %q is not allowed", ErrRefused, out.SubtitleLanguage)
		}
		args = append(args, "-metadata:s:s:0", "language="+out.SubtitleLanguage)
	}
	if out.Faststart {
		if out.Muxer != "mp4" {
			return nil, fmt.Errorf("%w: faststart applies to mp4 only", ErrRefused)
		}
		args = append(args, "-movflags", "+faststart")
	}
	return args, nil
}

// mapArgs turns Output.Maps into -map arguments: a graph label becomes
// [label], an input stream (1:a) stays as is.
func mapArgs(maps []string) ([]string, error) {
	var args []string
	for _, m := range maps {
		switch {
		case mapLabel.MatchString(m):
			args = append(args, "-map", "["+m+"]")
		case mapStream.MatchString(m):
			args = append(args, "-map", m)
		default:
			return nil, fmt.Errorf("%w: map %q is not allowed", ErrRefused, m)
		}
	}
	return args, nil
}

// imageInputArgs are the demuxer options of a looped still image.
func imageInputArgs(in Input) []string {
	var args []string
	if in.Loop {
		args = append(args, "-loop", "1")
	}
	if in.FrameRate > 0 {
		args = append(args, "-framerate", strconv.Itoa(in.FrameRate))
	}
	return args
}
