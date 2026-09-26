package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"loomtale/api/internal/obs/scrub"
)

// probeTimeout bounds one ffprobe run or encoder test encode.
const probeTimeout = 30 * time.Second

// ProbeEncoderArgs is the fixed argv of the one-frame test encode that
// tells whether codec works on this host (h264_nvenc needs a visible GPU
// with video capability). The source is a constant lavfi colour, so no
// input file or URL is involved.
func ProbeEncoderArgs(codec string) ([]string, error) {
	if codec != CodecX264 && codec != CodecNVENC {
		return nil, fmt.Errorf("%w: encoder %q is not probed", ErrRefused, codec)
	}
	return []string{
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=256x256:r=30",
		"-frames:v", "1", "-c:v", codec, "-f", MuxerNull, "-",
	}, nil
}

// ProbeEncoder runs the test encode; nil means codec is usable.
func (r *Runner) ProbeEncoder(ctx context.Context, codec string) error {
	args, err := ProbeEncoderArgs(codec)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	stderr := &cappedBuffer{limit: maxStderrBytes}
	cmd := exec.CommandContext(ctx, r.binary(), args...) //nolint:gosec // fixed argv, codec checked against a constant list
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %s test encode: %w: %s", codec, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Prober runs ffprobe on files inside a step's temp directory.
type Prober struct {
	// Binary is the ffprobe executable; defaults to "ffprobe" on PATH.
	Binary string
}

// ProbeStream is one stream of a probed file.
type ProbeStream struct {
	Index        int    `json:"index"`
	CodecType    string `json:"codec_type"`
	CodecName    string `json:"codec_name"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	AvgFrameRate string `json:"avg_frame_rate,omitempty"`
	NbFrames     string `json:"nb_frames,omitempty"`
	Duration     string `json:"duration,omitempty"`
}

// ProbeResult is the stream and container summary of a file.
type ProbeResult struct {
	Streams []ProbeStream `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// DurationMs is the container duration in milliseconds, or 0.
func (p *ProbeResult) DurationMs() int64 {
	f, err := strconv.ParseFloat(p.Format.Duration, 64)
	if err != nil || f < 0 {
		return 0
	}
	return int64(f*1000 + 0.5)
}

// Count returns how many streams of codecType ("video", "audio",
// "subtitle") the file has.
func (p *ProbeResult) Count(codecType string) int {
	n := 0
	for _, s := range p.Streams {
		if s.CodecType == codecType {
			n++
		}
	}
	return n
}

// ProbeArgs is the ffprobe argv for a local file of the given format.
func ProbeArgs(tempDir, path string, format Format) ([]string, error) {
	if err := insideDir(tempDir, path); err != nil {
		return nil, err
	}
	if !knownFormats[format] || format == FormatConcat {
		return nil, fmt.Errorf("%w: probe format %q is not allowed", ErrRefused, format)
	}
	return []string{
		"-hide_banner", "-loglevel", "error", "-protocol_whitelist", LocalProtocolWhitelist, "-f", string(format),
		"-show_entries", "stream=index,codec_type,codec_name,width,height,avg_frame_rate,nb_frames,duration:format=duration",
		"-of", "json", path,
	}, nil
}

// Probe reads the streams and duration of a local file.
func (p *Prober) Probe(ctx context.Context, tempDir, path string, format Format) (*ProbeResult, error) {
	args, err := ProbeArgs(tempDir, path, format)
	if err != nil {
		return nil, err
	}
	out, err := p.run(ctx, tempDir, args)
	if err != nil {
		return nil, err
	}
	var res ProbeResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("ffprobe: unreadable output: %w", err)
	}
	return &res, nil
}

// KeyframeTimes lists the presentation times (seconds) of the video
// keyframes of a local MP4, used to check that each segment boundary
// starts on a keyframe.
func (p *Prober) KeyframeTimes(ctx context.Context, tempDir, path string) ([]float64, error) {
	if err := insideDir(tempDir, path); err != nil {
		return nil, err
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-protocol_whitelist", LocalProtocolWhitelist, "-f", string(FormatMP4),
		"-select_streams", "v:0", "-skip_frame", "nokey", "-show_entries", "frame=pts_time", "-of", "csv=p=0", path,
	}
	out, err := p.run(ctx, tempDir, args)
	if err != nil {
		return nil, err
	}
	var times []float64
	for _, line := range strings.Fields(string(out)) {
		v, err := strconv.ParseFloat(strings.TrimSuffix(line, ","), 64)
		if err != nil {
			return nil, fmt.Errorf("ffprobe: unreadable keyframe time %q", line)
		}
		times = append(times, v)
	}
	return times, nil
}

func (p *Prober) run(ctx context.Context, dir string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	binary := p.Binary
	if binary == "" {
		binary = "ffprobe"
	}
	stderr := &cappedBuffer{limit: maxStderrBytes}
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec // argv is built from validated values only
	cmd.Dir = dir
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(scrub.URL(stderr.String())))
	}
	return out, nil
}
