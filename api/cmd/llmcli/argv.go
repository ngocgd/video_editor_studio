package main

// buildArgs returns the exact, fixed argv passed to the claude binary. It
// is a Go []string constant, never built from shell-parsed or
// user-influenced text: the prompt itself travels on stdin (see run.go),
// never as an argv element, and model/systemPrompt are both fixed,
// server-controlled values (never derived from request content). This is
// a from-scratch reimplementation of the documented CLI flags, not a copy
// of any reference implementation's argument-building code.
func buildArgs(model, systemPrompt string) []string {
	return []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--model", model,
		"--tools", "",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--setting-sources", "",
		"--disable-slash-commands",
		"--no-session-persistence",
		"--system-prompt", systemPrompt,
	}
}
