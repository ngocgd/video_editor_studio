package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"loomtale/api/internal/obs/scrub"
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
}

// Job is one ffmpeg invocation.
type Job struct {
	// TempDir is the step's own temp directory; every local path must be
	// inside it.
	TempDir string
	Inputs  []Input
	Output  Output
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
		return append(args, "-i", in.Path), nil
	case in.URL != "":
		if in.Format == FormatConcat {
			return nil, fmt.Errorf("%w: a concat list must be a local file", ErrRefused)
		}
		if err := r.checkRemote(in.URL); err != nil {
			return nil, err
		}
		return []string{"-protocol_whitelist", RemoteProtocolWhitelist, "-f", string(in.Format), "-i", in.URL}, nil
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

var allowedMuxers = map[string]bool{"webp": true, "avif": true, "wav": true, "s16le": true, "image2": true}
var allowedCodecs = map[string]bool{"": true, "libwebp": true, "libaom-av1": true, "libsvtav1": true, "pcm_s16le": true, "png": true}

func outputArgs(tempDir string, out Output) ([]string, error) {
	if err := insideDir(tempDir, out.Path); err != nil {
		return nil, err
	}
	if !allowedMuxers[out.Muxer] {
		return nil, fmt.Errorf("%w: output format %q is not allowed", ErrRefused, out.Muxer)
	}
	if !allowedCodecs[out.VideoCodec] || !allowedCodecs[out.AudioCodec] {
		return nil, fmt.Errorf("%w: codec is not allowed", ErrRefused)
	}
	var args []string
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
	if out.Channels > 0 {
		args = append(args, "-ac", strconv.Itoa(out.Channels))
	}
	if out.SampleRate > 0 {
		args = append(args, "-ar", strconv.Itoa(out.SampleRate))
	}
	return append(args, "-f", out.Muxer, out.Path), nil
}

// Run executes job. A non-zero exit returns an error carrying the capped,
// URL-scrubbed stderr; a refused job never starts a process.
func (r *Runner) Run(ctx context.Context, job Job) error {
	args, err := r.Args(job)
	if err != nil {
		return err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	binary := r.Binary
	if binary == "" {
		binary = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec // argv is built from typed, validated values only (see Args)
	cmd.Dir = job.TempDir
	stderr := &cappedBuffer{limit: maxStderrBytes}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg: start: %w", err)
	}
	parseProgress(stdout, job.TotalMs, job.OnProgress)
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(scrub.URL(stderr.String()))
		if ctx.Err() != nil {
			return fmt.Errorf("ffmpeg: timed out or cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, msg)
	}
	return nil
}

// parseProgress reads ffmpeg's -progress key=value stream until EOF and
// reports out_time as a percentage of totalMs.
func parseProgress(r io.Reader, totalMs int64, onProgress func(int)) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok || onProgress == nil || totalMs <= 0 {
			continue
		}
		if key == "out_time_us" || key == "out_time_ms" {
			// Both keys carry microseconds (out_time_ms is misnamed upstream).
			us, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil || us < 0 {
				continue
			}
			pct := int(us / 1000 * 100 / totalMs)
			onProgress(min(pct, 99))
		}
		if key == "progress" && value == "end" {
			onProgress(100)
		}
	}
}

type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }

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
