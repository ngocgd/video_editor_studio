package pipeline

import (
	"context"
	"strings"
	"sync"

	"loomtale/api/internal/obs/scrub"
)

// maxLogLines bounds the in-memory ring buffer a step's log accumulates
// during one attempt: workers can log verbosely (FFmpeg, ComfyUI, Python
// tracebacks) without unbounded memory growth. Oldest lines are dropped
// first; the flushed asset always carries the tail of the log, which is
// what matters for diagnosing why a step failed.
const maxLogLines = 2000

// logRing is a bounded, scrubbed log buffer. Every line is scrubbed for
// secrets and capability URLs (see obs/scrub) before it is ever held in
// memory, not just before it is flushed, so a crash mid-run cannot leave
// an unscrubbed buffer anywhere for a debugger to dump.
type logRing struct {
	mu      sync.Mutex
	lines   []string
	dropped int
}

func newLogRing() *logRing {
	return &logRing{lines: make([]string, 0, 64)}
}

func (r *logRing) add(line string) {
	scrubbed := scrub.Text(line)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.lines) >= maxLogLines {
		r.lines = r.lines[1:]
		r.dropped++
	}
	r.lines = append(r.lines, scrubbed)
}

func (r *logRing) bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	if r.dropped > 0 {
		b.WriteString("[")
		b.WriteString(itoa(r.dropped))
		b.WriteString(" earlier lines dropped]\n")
	}
	for _, l := range r.lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// logSink uploads a flushed log buffer to object storage and returns the
// asset id it was recorded under, or (uuid.Nil, nil) if there is nothing
// to flush. It is an interface (rather than a concrete storage.Internal
// dependency baked into StepContext) so unit tests can exercise
// StepContext.Log without a live MinIO.
type logSink interface {
	// FlushLog uploads body for the given step/attempt and records it as
	// an asset, returning the new asset id.
	FlushLog(ctx context.Context, tenantID, stepID string, attempt int32, body []byte) (assetID string, err error)
}
