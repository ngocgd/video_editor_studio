package main

import (
	"reflect"
	"testing"
)

// TestBuildArgsGoldenArgv locks the exact argv the shim passes to the
// claude binary (recorded during the sidecar auth spike, per the phase
// contract): any accidental change here is a breaking change to the
// isolation guarantees (tools disabled, no MCP servers, no persisted
// session, no slash commands).
func TestBuildArgsGoldenArgv(t *testing.T) {
	got := buildArgs("claude-sonnet-5", "You are a helpful assistant.")
	want := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "claude-sonnet-5",
		"--tools", "",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--setting-sources", "",
		"--disable-slash-commands",
		"--no-session-persistence",
		"--system-prompt", "You are a helpful assistant.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs() =\n%#v\nwant\n%#v", got, want)
	}
}

func TestBuildArgsNeverEmitsShellMetacharactersUnescaped(t *testing.T) {
	// The prompt never appears in argv at all (it travels on stdin), so
	// this only needs to confirm the system prompt element is passed
	// through verbatim as a single argv element, never concatenated into
	// a shell string that could be re-interpreted.
	args := buildArgs("m", "prompt with $(rm -rf /) inside")
	found := false
	for _, a := range args {
		if a == "prompt with $(rm -rf /) inside" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected system prompt to appear as a single unmodified argv element")
	}
}
