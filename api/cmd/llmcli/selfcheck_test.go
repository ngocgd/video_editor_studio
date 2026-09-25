package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeFakeSelfCheckBinary(t *testing.T, version, helpOutput string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake claude binary requires a POSIX shell")
	}
	script := "#!/bin/sh\n" +
		`if [ "$1" = "--version" ]; then echo '` + version + "'; exit 0; fi\n" +
		`if [ "$1" = "-p" ] && [ "$2" = "--help" ]; then cat <<'EOF'
` + helpOutput + `
EOF
exit 0
fi
exit 1
`
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

const fullHelpOutput = "Usage: claude [options]\n  --tools <names>  allowed tools\n  --strict-mcp-config  only use configured MCP servers"

func TestSelfCheckPassesWithMatchingVersionAndFlags(t *testing.T) {
	binary := writeFakeSelfCheckBinary(t, "2.1.282 (Claude Code)", fullHelpOutput)
	version, err := selfCheck(context.Background(), binary, "2.1.282")
	if err != nil {
		t.Fatal(err)
	}
	if version == "" {
		t.Fatal("expected a non-empty version string")
	}
}

func TestSelfCheckFailsOnVersionMismatch(t *testing.T) {
	binary := writeFakeSelfCheckBinary(t, "1.0.0 (Claude Code)", fullHelpOutput)
	if _, err := selfCheck(context.Background(), binary, "2.1.282"); err == nil {
		t.Fatal("expected a version-pin mismatch to fail the self-check")
	}
}

// TestSelfCheckFailsWhenRequiredFlagIsMissing is M3: a CLI upgrade that
// drops --tools or --strict-mcp-config must fail loudly, not silently
// run with the isolation guarantee unverified.
func TestSelfCheckFailsWhenRequiredFlagIsMissing(t *testing.T) {
	binary := writeFakeSelfCheckBinary(t, "2.1.282 (Claude Code)", "Usage: claude [options]\n  --model <name>")
	if _, err := selfCheck(context.Background(), binary, "2.1.282"); err == nil {
		t.Fatal("expected the self-check to fail when --tools/--strict-mcp-config are missing from --help")
	}
}
