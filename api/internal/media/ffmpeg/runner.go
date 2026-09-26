package ffmpeg

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"loomtale/api/internal/pipeline"
)

// maxStderrBytes caps how much of ffmpeg's stderr is kept for the error
// message and the step log; -loglevel error keeps it short anyway.
const maxStderrBytes = 16 << 10

// defaultTimeout bounds one ffmpeg run when the caller sets none.
const defaultTimeout = 10 * time.Minute

// ErrRefused is a request the runner refuses to execute (a path outside
// the temp directory, a foreign host, a forbidden protocol): permanent,
// since retrying the same input can never pass.
var ErrRefused = fmt.Errorf("ffmpeg: refused: %w", pipeline.ErrValidation)

// Input is one -i input: exactly one of Path (a file inside the job's
// temp directory) or URL (an internal presigned https URL).
type Input struct {
	Format Format
	Path   string
	URL    string
	// Loop repeats a still image input forever (-loop 1); the output's
	// frame count ends it.
	Loop bool
	// FrameRate is the rate an image input is read at (-framerate).
	FrameRate int
}

// Output describes the single output file with typed options only.
type Output struct {
	// Path must be inside the job's temp directory.
	Path string
	// Muxer is the forced output format (-f): webp, avif, wav, s16le.
	Muxer string
	// VideoCodec/AudioCodec select encoders (-c:v / -c:a).
	VideoCodec string
	AudioCodec string
	// ScaleWidth, when positive, scales video to that width keeping the
	// aspect ratio (height rounded to an even number).
	ScaleWidth int
	// Quality is -q:v for libwebp and -crf for AV1.
	Quality int
	// StillPicture makes an AV1 encoder produce a single still image.
	StillPicture bool
	// Frames limits the number of video frames written.
	Frames int
	// Channels/SampleRate resample audio (-ac / -ar).
	Channels   int
	SampleRate int
	// NoVideo/NoAudio drop that stream (-vn / -an).
	NoVideo bool
	NoAudio bool

	// Maps selects output streams (-map): a graph label such as "v" or
	// an input stream such as "1:a". Empty keeps ffmpeg's default pick.
	Maps []string
	// Video holds the H.264 settings of a render segment or proxy.
	Video *VideoEncode
	// AudioBitrateK is the AAC bitrate in kbit/s (-b:a).
	AudioBitrateK int
	// SubtitleCodec encodes a subtitle input (-c:s), e.g. mov_text.
	SubtitleCodec string
	// SubtitleLanguage is the ISO 639-2 code of the subtitle stream.
	SubtitleLanguage string
	// Faststart moves the MP4 index to the front (-movflags +faststart).
	Faststart bool
}

// Job is one ffmpeg invocation.
type Job struct {
	// TempDir is the step's own temp directory; every local path must be
	// inside it.
	TempDir string
	Inputs  []Input
	// Graph, when set, is passed as -filter_complex; outputs then pick
	// its labels with Output.Maps.
	Graph  *Graph
	Output Output
	// TotalMs, when positive, turns ffmpeg's progress output into a
	// percentage for OnProgress.
	TotalMs    int64
	OnProgress func(pct int)
}

// Runner executes Jobs.
type Runner struct {
	// Binary is the ffmpeg executable; defaults to "ffmpeg" on PATH.
	Binary string
	// RemoteHost is the only host a remote input may point at
	// (host:port of the internal MinIO endpoint). Empty refuses every
	// remote input.
	RemoteHost string
	Timeout    time.Duration
}

// Args validates job and returns the argv (without the binary) it runs.
func (r *Runner) Args(job Job) ([]string, error) {
	if job.TempDir == "" || !filepath.IsAbs(job.TempDir) {
		return nil, fmt.Errorf("%w: the temp directory must be an absolute path", ErrRefused)
	}
	if len(job.Inputs) == 0 {
		return nil, fmt.Errorf("%w: no input", ErrRefused)
	}
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y"}
	for _, in := range job.Inputs {
		inArgs, err := r.inputArgs(job.TempDir, in)
		if err != nil {
			return nil, err
		}
		args = append(args, inArgs...)
	}
	if job.Graph != nil {
		if job.Output.ScaleWidth > 0 {
			return nil, fmt.Errorf("%w: a filtergraph job scales inside its graph", ErrRefused)
		}
		g, err := job.Graph.String()
		if err != nil {
			return nil, err
		}
		args = append(args, "-filter_complex", g)
	}
	outArgs, err := outputArgs(job.TempDir, job.Output)
	if err != nil {
		return nil, err
	}
	args = append(args, "-progress", "pipe:1")
	return append(args, outArgs...), nil
}

func (r *Runner) inputArgs(tempDir string, in Input) ([]string, error) {
	if !knownFormats[in.Format] {
		return nil, fmt.Errorf("%w: input format %q is not allowed", ErrRefused, in.Format)
	}
	if (in.Loop || in.FrameRate != 0) && (in.Format != FormatImage2 || in.FrameRate < 0) {
		return nil, fmt.Errorf("%w: loop and frame rate apply to image inputs only", ErrRefused)
	}
	switch {
	case in.Path != "" && in.URL != "":
		return nil, fmt.Errorf("%w: an input is either a file or a URL", ErrRefused)
	case in.Path != "":
		if err := insideDir(tempDir, in.Path); err != nil {
			return nil, err
		}
		args := []string{"-protocol_whitelist", LocalProtocolWhitelist, "-f", string(in.Format)}
		if in.Format == FormatConcat {
			args = append(args, "-safe", "1")
		}
		args = append(args, imageInputArgs(in)...)
		return append(args, "-i", in.Path), nil
	case in.URL != "":
		if in.Format == FormatConcat {
			return nil, fmt.Errorf("%w: a concat list must be a local file", ErrRefused)
		}
		if err := r.checkRemote(in.URL); err != nil {
			return nil, err
		}
		args := []string{"-protocol_whitelist", RemoteProtocolWhitelist, "-f", string(in.Format)}
		args = append(args, imageInputArgs(in)...)
		return append(args, "-i", in.URL), nil
	default:
		return nil, fmt.Errorf("%w: an input needs a file or a URL", ErrRefused)
	}
}

// checkRemote accepts only an https URL to the configured internal host.
func (r *Runner) checkRemote(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: unparsable input URL", ErrRefused)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: input URL scheme %q is not https", ErrRefused, u.Scheme)
	}
	if u.User != nil || u.Host == "" || r.RemoteHost == "" || !strings.EqualFold(u.Host, r.RemoteHost) {
		return fmt.Errorf("%w: input URL host is not the internal object store", ErrRefused)
	}
	return nil
}

// insideDir accepts only an absolute, clean path strictly inside dir.
func insideDir(dir, p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return fmt.Errorf("%w: %q is not a clean absolute path", ErrRefused, filepath.Base(p))
	}
	rel, err := filepath.Rel(filepath.Clean(dir), p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: %q is outside the step temp directory", ErrRefused, filepath.Base(p))
	}
	return nil
}

var allowedMuxers = map[string]bool{
	"webp": true, "avif": true, "wav": true, "s16le": true, "image2": true, "mp4": true, MuxerNull: true,
}
var allowedCodecs = map[string]bool{
	"": true, "libwebp": true, "libaom-av1": true, "libsvtav1": true, "pcm_s16le": true, "png": true,
	CodecX264: true, CodecNVENC: true, "aac": true, CodecCopy: true,
}
var allowedSubtitleCodecs = map[string]bool{"": true, "mov_text": true, CodecCopy: true}

// MuxerNull discards the output; its Path must be empty. Measuring runs
// (loudnorm first pass, ebur128) use it.
const MuxerNull = "null"

func outputArgs(tempDir string, out Output) ([]string, error) {
	target := out.Path
	if out.Muxer == MuxerNull {
		if out.Path != "" {
			return nil, fmt.Errorf("%w: a null output has no path", ErrRefused)
		}
		target = "-"
	} else if err := insideDir(tempDir, out.Path); err != nil {
		return nil, err
	}
	if !allowedMuxers[out.Muxer] {
		return nil, fmt.Errorf("%w: output format %q is not allowed", ErrRefused, out.Muxer)
	}
	if !allowedCodecs[out.VideoCodec] || !allowedCodecs[out.AudioCodec] || !allowedSubtitleCodecs[out.SubtitleCodec] {
		return nil, fmt.Errorf("%w: codec is not allowed", ErrRefused)
	}
	args, err := mapArgs(out.Maps)
	if err != nil {
		return nil, err
	}
	if out.NoVideo {
		args = append(args, "-vn")
	}
	if out.NoAudio {
		args = append(args, "-an")
	}
	if out.ScaleWidth > 0 {
		// Only an integer is interpolated into the filter string.
		args = append(args, "-vf", "scale="+strconv.Itoa(out.ScaleWidth)+":-2")
	}
	if out.VideoCodec != "" {
		args = append(args, "-c:v", out.VideoCodec)
	}
	videoArgs, err := out.Video.args(out.VideoCodec)
	if err != nil {
		return nil, err
	}
	args = append(args, videoArgs...)
	if out.StillPicture {
		args = append(args, "-still-picture", "1")
	}
	if out.Quality > 0 {
		switch out.VideoCodec {
		case "libwebp":
			args = append(args, "-q:v", strconv.Itoa(out.Quality))
		case "libaom-av1", "libsvtav1":
			args = append(args, "-crf", strconv.Itoa(out.Quality))
		}
	}
	if out.Frames > 0 {
		args = append(args, "-frames:v", strconv.Itoa(out.Frames))
	}
	if out.AudioCodec != "" {
		args = append(args, "-c:a", out.AudioCodec)
	}
	if out.AudioBitrateK > 0 {
		args = append(args, "-b:a", strconv.Itoa(out.AudioBitrateK)+"k")
	}
	if out.Channels > 0 {
		args = append(args, "-ac", strconv.Itoa(out.Channels))
	}
	if out.SampleRate > 0 {
		args = append(args, "-ar", strconv.Itoa(out.SampleRate))
	}
	containerArgs, err := out.containerArgs()
	if err != nil {
		return nil, err
	}
	args = append(args, containerArgs...)
	return append(args, "-f", out.Muxer, target), nil
}

// WriteConcatList writes a concat demuxer list of plain file names (no
// directories, resolved relative to the list itself) into dir and returns
// its path. Names with a separator, "..", or a protocol prefix are
// refused, so a list can only ever point at files next to it.
func WriteConcatList(dir, listName string, names []string, write func(path string, data []byte) error) (string, error) {
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for _, n := range names {
		if n == "" || n != filepath.Base(n) || n == ".." || strings.ContainsAny(n, "':\\/\n\r") {
			return "", fmt.Errorf("%w: concat entry %q is not a plain file name", ErrRefused, n)
		}
		b.WriteString("file '")
		b.WriteString(n)
		b.WriteString("'\n")
	}
	p := filepath.Join(dir, listName)
	if err := insideDir(dir, p); err != nil {
		return "", err
	}
	if err := write(p, []byte(b.String())); err != nil {
		return "", err
	}
	return p, nil
}

// IsRefused reports whether err is a runner refusal.
func IsRefused(err error) bool { return errors.Is(err, ErrRefused) }
