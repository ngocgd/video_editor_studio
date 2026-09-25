package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"loomtale/api/internal/obs/scrub"
)

// maxStderrCapture bounds how much of the CLI's stderr this process ever
// keeps in memory, per the phase contract (64KB capped and scrubbed).
const maxStderrCapture = 64 * 1024

// waitDelay bounds how long Wait keeps the process's I/O pipes open after
// the process itself exits or ctx is cancelled, before force-killing the
// whole process group.
const waitDelay = 5 * time.Second

// runRequest is one llm-cli invocation.
type runRequest struct {
	Model        string
	SystemPrompt string
	Prompt       string // sent on stdin, never argv
	OAuthToken   string
}

// runResult is the outcome of one invocation, ready to be relayed to the
// caller as NDJSON deltas plus a final summary.
type runResult struct {
	parseResult
	StderrTail string
}

// runner executes claude CLI invocations under a bounded concurrency
// limit and a fresh, isolated working directory per call.
type runner struct {
	binary   string
	workRoot string
	sem      chan struct{}
	timeout  time.Duration
}

// newRunner builds a runner. maxConcurrent defaults to 2 per the phase
// contract; timeout defaults to 10 minutes.
func newRunner(binary, workRoot string, maxConcurrent int, timeout time.Duration) *runner {
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &runner{binary: binary, workRoot: workRoot, sem: make(chan struct{}, maxConcurrent), timeout: timeout}
}

// Run spawns one claude CLI process, streams its stdout through
// parseNDJSONStream (onDelta fires as text arrives), and returns the
// aggregated result. It never returns until the process has fully exited
// (or been killed), so the caller can safely clean up the workdir
// afterwards.
func (rn *runner) Run(ctx context.Context, req runRequest, onDelta func(string)) (runResult, error) {
	select {
	case rn.sem <- struct{}{}:
		defer func() { <-rn.sem }()
	case <-ctx.Done():
		return runResult{}, ctx.Err()
	}

	runCtx, cancel := context.WithTimeout(ctx, rn.timeout)
	defer cancel()

	workdir, err := os.MkdirTemp(rn.workRoot, "llmcli-*")
	if err != nil {
		return runResult{}, fmt.Errorf("llmcli: create workdir: %w", err)
	}
	if err := os.Chmod(workdir, 0o700); err != nil {
		_ = os.RemoveAll(workdir)
		return runResult{}, fmt.Errorf("llmcli: chmod workdir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workdir) }()

	args := buildArgs(req.Model, req.SystemPrompt)
	cmd := exec.CommandContext(runCtx, rn.binary, args...)
	cmd.Dir = workdir
	cmd.Env = filterCLIEnv(req.OAuthToken)
	cmd.WaitDelay = waitDelay
	// Kill the whole process tree on cancellation/timeout, not just the
	// immediate child: the CLI may spawn its own subprocesses (e.g. the
	// Node runtime), and killing only the parent pid would leave them
	// running past the request's own lifetime. configureProcGroup is
	// platform-specific (process-group SIGTERM on Unix, taskkill /T on
	// Windows — the host-side fallback runs natively on Windows).
	configureProcGroup(cmd)

	cmd.Stdin = strings.NewReader(req.Prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return runResult{}, fmt.Errorf("llmcli: stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &capWriter{buf: &stderrBuf, limit: maxStderrCapture}

	if err := cmd.Start(); err != nil {
		return runResult{}, fmt.Errorf("llmcli: start claude: %w", err)
	}

	parsed, parseErr := parseNDJSONStream(stdout, onDelta)

	if parseErr != nil {
		// Any parse failure (tools-enabled, an oversized response, a
		// malformed line, an event arriving before system/init, or ctx
		// cancellation itself closing the stdout pipe and surfacing as a
		// read error here) means stdout can no longer be trusted and the
		// process must die immediately. Leaving it running relies on the
		// 10-minute context timeout to eventually reap it, which stalls
		// the whole semaphore slot (default 2 concurrent) and keeps
		// burning subscription quota in the meantime.
		killProcessGroup(cmd)
		_ = cmd.Wait()
		return runResult{StderrTail: scrub.Text(stderrBuf.String())}, parseErr
	}

	waitErr := cmd.Wait()
	result := runResult{parseResult: parsed, StderrTail: scrub.Text(stderrBuf.String())}

	if waitErr != nil {
		return result, fmt.Errorf("llmcli: claude process failed: %w (stderr: %s)", waitErr, result.StderrTail)
	}
	if !parsed.InitOK {
		return result, fmt.Errorf("llmcli: claude exited without a system/init event")
	}
	return result, nil
}

// capWriter caps how many bytes it retains, discarding the rest, so a
// runaway stderr stream can never grow unbounded memory.
type capWriter struct {
	buf   *bytes.Buffer
	limit int
}

func (w *capWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if remaining > 0 {
		n := len(p)
		if n > remaining {
			n = remaining
		}
		w.buf.Write(p[:n])
	}
	return len(p), nil
}

var _ io.Writer = (*capWriter)(nil)
