package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func writeFakeClaudeScript(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake claude script requires a POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRunKillsProcessOnOversizedOutputInsteadOfWaitingForTimeout is H3:
// any parse error (not just tools-enabled) must kill the process
// immediately, not fall through to cmd.Wait() on a process that is
// still writing to a full stdout pipe (or still generating) and would
// otherwise only be reaped by the 10-minute context timeout.
func TestRunKillsProcessOnOversizedOutputInsteadOfWaitingForTimeout(t *testing.T) {
	// Emits well over the 2MB total-output cap in one line, then sleeps
	// far longer than this test's own deadline: if run.go did not kill
	// the process on the resulting ErrOutputTooLarge, Run would block
	// until the sleep (or the 10-minute runner timeout) elapsed.
	script := "#!/bin/sh\ncat >/dev/null\n" +
		`echo '{"type":"system","subtype":"init","tools":[],"mcp_servers":[]}'` + "\n" +
		`BIG=$(head -c 3000000 /dev/zero | tr '\0' 'x')` + "\n" +
		`printf '{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}]}}\n' "$BIG"` + "\n" +
		"sleep 30\n"

	binary := writeFakeClaudeScript(t, script)
	rn := newRunner(binary, t.TempDir(), 2, 30*time.Second)

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := rn.Run(context.Background(), runRequest{Model: "m", SystemPrompt: "p", Prompt: "hi"}, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrOutputTooLarge) {
			t.Fatalf("expected ErrOutputTooLarge, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("Run took %s; expected the process to be killed promptly, not wait for its sleep", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return promptly after the output cap was exceeded; the process was not killed")
	}
}

// TestRunKillsProcessOnContextCancel is decision 6: cancelling the
// caller's context must release the runner's semaphore slot promptly
// rather than waiting for the process's own exit.
func TestRunKillsProcessOnContextCancel(t *testing.T) {
	script := "#!/bin/sh\ncat >/dev/null\n" +
		`echo '{"type":"system","subtype":"init","tools":[],"mcp_servers":[]}'` + "\n" +
		"sleep 30\n"
	binary := writeFakeClaudeScript(t, script)
	rn := newRunner(binary, t.TempDir(), 2, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := rn.Run(ctx, runRequest{Model: "m", SystemPrompt: "p", Prompt: "hi"}, nil)
		done <- err
	}()

	time.Sleep(200 * time.Millisecond) // let the process start and pass init
	cancel()

	select {
	case <-done:
		// returned promptly; the slot is free again either way.
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return promptly after context cancellation")
	}

	// The semaphore slot must be free again: a second Run should not
	// block waiting on the cancelled call's slot.
	select {
	case rn.sem <- struct{}{}:
		<-rn.sem
	default:
		t.Fatal("semaphore slot was not released after context cancellation")
	}
}
