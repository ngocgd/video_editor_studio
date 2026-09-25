package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeFakeClaude writes a shell script standing in for the real claude
// binary, so handleRun can be exercised end-to-end (argv building, env
// filtering, stdin, NDJSON parsing, HTTP relay) without a real CLI or
// network access. Skipped on non-Unix hosts since it relies on a shebang
// script and SIGTERM-based process-group handling identical to run.go.
func writeFakeClaude(t *testing.T, script string) string {
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

func newTestHandler(t *testing.T, script string) *handler {
	t.Helper()
	binary := writeFakeClaude(t, script)
	return &handler{
		runner:       newRunner(binary, t.TempDir(), 2, 5*time.Second),
		model:        "claude-test",
		systemPrompt: "fixed prompt",
		oauthToken:   "test-token",
		bearerToken:  "shared-secret",
	}
}

func TestHandleRunStreamsDeltasAndResult(t *testing.T) {
	script := "#!/bin/sh\ncat >/dev/null\n" +
		`echo '{"type":"system","subtype":"init","tools":[],"mcp_servers":[]}'` + "\n" +
		`echo '{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}'` + "\n" +
		`echo '{"type":"result","is_error":false,"result":"hi","total_cost_usd":0.001,"usage":{"input_tokens":3,"output_tokens":1}}'` + "\n"
	h := newTestHandler(t, script)

	req := httptest.NewRequest(http.MethodPost, "/v1/run", strings.NewReader(`{"prompt":"hello"}`))
	req.Header.Set("Authorization", "Bearer shared-secret")
	rec := httptest.NewRecorder()

	h.handleRun(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var lines []streamLine
	scanner := bufio.NewScanner(rec.Body)
	for scanner.Scan() {
		var l streamLine
		if err := json.Unmarshal(scanner.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, l)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %+v", len(lines), lines)
	}
	if lines[0].Type != "delta" || lines[0].Text != "hi" {
		t.Fatalf("delta line = %+v", lines[0])
	}
	if lines[1].Type != "result" || lines[1].IsError || lines[1].Text != "hi" {
		t.Fatalf("result line = %+v", lines[1])
	}
}

func TestHandleRunRejectsWrongBearerToken(t *testing.T) {
	h := newTestHandler(t, "#!/bin/sh\nexit 1\n")
	req := httptest.NewRequest(http.MethodPost, "/v1/run", strings.NewReader(`{"prompt":"hello"}`))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()

	h.handleRun(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleRunKillsProcessWhenToolsEnabled(t *testing.T) {
	script := "#!/bin/sh\ncat >/dev/null\n" +
		`echo '{"type":"system","subtype":"init","tools":["bash"],"mcp_servers":[]}'` + "\n" +
		"sleep 30\n"
	h := newTestHandler(t, script)

	req := httptest.NewRequest(http.MethodPost, "/v1/run", strings.NewReader(`{"prompt":"hello"}`))
	req.Header.Set("Authorization", "Bearer shared-secret")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.handleRun(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handleRun did not return promptly after tools-enabled detection; process was not killed")
	}

	var lastLine streamLine
	scanner := bufio.NewScanner(rec.Body)
	for scanner.Scan() {
		_ = json.Unmarshal(scanner.Bytes(), &lastLine)
	}
	if !lastLine.IsError || lastLine.Error != "cli_tools_enabled" {
		t.Fatalf("expected cli_tools_enabled result, got %+v", lastLine)
	}
}
