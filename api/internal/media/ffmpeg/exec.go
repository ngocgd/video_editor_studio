package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"loomtale/api/internal/obs/scrub"
)

// maxLogBytes is how much of a RunLog log is kept. Measuring filters
// print their summary last, so the tail is what matters.
const maxLogBytes = 64 << 10

// Run executes job. A non-zero exit returns an error carrying the capped,
// URL-scrubbed stderr; a refused job never starts a process.
func (r *Runner) Run(ctx context.Context, job Job) error {
	args, err := r.Args(job)
	if err != nil {
		return err
	}
	return r.exec(ctx, job, args, &cappedBuffer{limit: maxStderrBytes})
}

// RunLog executes job at -loglevel info and returns the URL-scrubbed tail
// of its log, where measuring filters (loudnorm, ebur128) print results.
func (r *Runner) RunLog(ctx context.Context, job Job) (string, error) {
	args, err := r.Args(job)
	if err != nil {
		return "", err
	}
	// Args always carries "-loglevel error"; raise it and drop the
	// periodic stats line so the log holds filter output only.
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-loglevel" {
			args[i+1] = "info"
			break
		}
	}
	args = append([]string{"-nostats"}, args...)
	tail := &tailBuffer{limit: maxLogBytes}
	if err := r.exec(ctx, job, args, tail); err != nil {
		return "", err
	}
	return scrub.URL(tail.String()), nil
}

type logSink interface {
	io.Writer
	String() string
}

func (r *Runner) exec(ctx context.Context, job Job, args []string, stderr logSink) error {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.binary(), args...) //nolint:gosec // argv is built from typed, validated values only (see Args)
	cmd.Dir = job.TempDir
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
		if len(msg) > maxStderrBytes {
			msg = msg[len(msg)-maxStderrBytes:]
		}
		if ctx.Err() != nil {
			return fmt.Errorf("ffmpeg: timed out or cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, msg)
	}
	return nil
}

func (r *Runner) binary() string {
	if r.Binary == "" {
		return "ffmpeg"
	}
	return r.Binary
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

// cappedBuffer keeps the first limit bytes written to it.
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

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	buf   []byte
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.limit; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }
