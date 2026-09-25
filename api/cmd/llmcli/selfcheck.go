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

// selfCheck runs `claude --version` once at startup and confirms the
// binary is present and responds; a failure here disables the whole
// provider (the caller reports why via /healthz) instead of accepting
// requests it cannot actually serve. It does not attempt a live
// authenticated call (that requires a real token and network egress,
// exercised separately in integration tests), only that the pinned CLI
// exists and its version banner parses.
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
	return version, nil
}
