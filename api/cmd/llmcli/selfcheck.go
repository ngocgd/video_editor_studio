package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// selfCheckTimeout bounds the startup `claude --version` probe.
const selfCheckTimeout = 15 * time.Second

// requiredFlags is probed against `claude -p --help` output: the two
// flags this shim depends on for isolation (no tools, no MCP servers).
// A CLI upgrade that renames or drops either must fail the self-check
// loudly rather than silently running with the isolation guarantee
// unverified.
var requiredFlags = []string{"--tools", "--strict-mcp-config"}

// selfCheck runs `claude --version` plus a flag probe (`claude -p
// --help`) once at startup and confirms the binary is present, responds,
// and still exposes the flags buildArgs relies on; a failure here
// disables the whole provider (the caller reports why via /healthz)
// instead of accepting requests it cannot actually serve. It does not
// attempt a live authenticated call (that requires a real token and
// network egress, exercised separately in integration tests), only that
// the pinned CLI exists, its version banner parses, and its flag surface
// still matches what buildArgs assumes.
func selfCheck(ctx context.Context, binary, pinnedVersion string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, selfCheckTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("llmcli: claude --version failed: %w", err)
	}
	version := strings.TrimSpace(string(out))
	if pinnedVersion != "" && !strings.Contains(version, pinnedVersion) {
		return version, fmt.Errorf("llmcli: claude version %q does not match pinned version %q", version, pinnedVersion)
	}

	helpCmd := exec.CommandContext(ctx, binary, "-p", "--help")
	helpOut, err := helpCmd.Output()
	if err != nil {
		return version, fmt.Errorf("llmcli: claude -p --help failed: %w", err)
	}
	help := string(helpOut)
	for _, flag := range requiredFlags {
		if !strings.Contains(help, flag) {
			return version, fmt.Errorf("llmcli: claude -p --help no longer lists %q; the pinned CLI's flag surface changed", flag)
		}
	}
	return version, nil
}
