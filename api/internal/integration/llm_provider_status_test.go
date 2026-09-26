//go:build integration

package integration

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"loomtale/api/internal/db/gen"
)

type cliStatusBody struct {
	Installed     bool    `json:"installed"`
	Authenticated bool    `json:"authenticated"`
	ToolsDisabled bool    `json:"toolsDisabled"`
	Version       *string `json:"version"`
	Detail        *string `json:"detail"`
}

// TestClaudeCLIStatusComesFromTheWorker checks Settings > LLM against the
// running stack: the api has no route to the llm-cli sidecar, so the
// claude CLI status and claude-cli availability must come from the
// worker's heartbeat and agree with each other. With LLMCLI_EXPECTED=1
// (the stack was started with COMPOSE_PROFILES=claude-cli) the sidecar
// must be reported installed with its version, and when it is
// authenticated the Test button must succeed through the worker.
func TestClaudeCLIStatusComesFromTheWorker(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "llm-cli-status", uniqueEmail("llm-cli-status"), "owner")
	sess := login(t, fx.Email, fx.Password)

	// The worker publishes its first heartbeat right after boot; allow a
	// few beats for a stack that has only just come up.
	var status cliStatusBody
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp := sess.do(http.MethodGet, "/settings/llm/cli-status", nil)
		requireStatus(t, resp, http.StatusOK)
		decodeJSON(t, resp, &status)
		offline := status.Detail != nil && strings.HasPrefix(*status.Detail, "worker offline")
		if !offline || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	if status.Detail != nil && strings.HasPrefix(*status.Detail, "worker offline") {
		t.Fatalf("the worker never reported a heartbeat: %s", *status.Detail)
	}
	if !status.ToolsDisabled {
		t.Fatal("toolsDisabled must always be true")
	}

	resp := sess.do(http.MethodGet, "/settings/llm", nil)
	requireStatus(t, resp, http.StatusOK)
	var settings struct {
		Providers []struct {
			Name           string  `json:"name"`
			Available      bool    `json:"available"`
			DisabledReason *string `json:"disabledReason"`
		} `json:"providers"`
	}
	decodeJSON(t, resp, &settings)
	claudeAvailable := false
	for _, p := range settings.Providers {
		if p.Name == "claude-cli" {
			claudeAvailable = p.Available
			if !p.Available && (p.DisabledReason == nil || *p.DisabledReason == "") {
				t.Fatal("an unavailable claude-cli must carry the worker's reason")
			}
		}
	}
	if want := status.Installed && status.Authenticated; claudeAvailable != want {
		t.Fatalf("claude-cli available=%v but cli-status installed=%v authenticated=%v", claudeAvailable, status.Installed, status.Authenticated)
	}

	if os.Getenv("LLMCLI_EXPECTED") != "1" {
		return
	}
	if !status.Installed || status.Version == nil || *status.Version == "" {
		t.Fatalf("with the llm-cli sidecar running, expected installed with a version, got %+v", status)
	}
	if !status.Authenticated {
		return
	}
	// The server bounds this call at 30s and a probe can queue behind
	// in-flight generations inside the sidecar, so wait longer than that
	// rather than timing out a healthy provider on a busy stack.
	testResp := sess.doWithin(45*time.Second, http.MethodPost, "/settings/llm/test", map[string]any{"provider": "claude-cli"})
	requireStatus(t, testResp, http.StatusOK)
	var result struct {
		Ok     bool    `json:"ok"`
		Detail *string `json:"detail"`
	}
	decodeJSON(t, testResp, &result)
	if !result.Ok {
		detail := ""
		if result.Detail != nil {
			detail = *result.Detail
		}
		t.Fatalf("Test through the worker failed for a healthy claude-cli: %s", detail)
	}
}
