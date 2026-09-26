package main

import (
	"context"
	"errors"
	"testing"

	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/llm/claudecli"
)

type fakeLLM struct{ name string }

func (f fakeLLM) Name() string { return f.name }
func (f fakeLLM) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}
func (f fakeLLM) Stream(context.Context, llm.Request, func(llm.Delta)) (llm.Response, error) {
	return llm.Response{}, nil
}

type fakeCLI struct {
	fakeLLM
	status claudecli.CLIStatus
	err    error
}

func (f fakeCLI) Status(context.Context) (claudecli.CLIStatus, error) { return f.status, f.err }

func TestLLMProviderStatusReportsHealthyCLIWithVersion(t *testing.T) {
	got := llmProviderStatus(context.Background(), map[string]llm.Provider{
		"claude-cli":    fakeCLI{status: claudecli.CLIStatus{Installed: true, Authenticated: true, Version: "2.1.282 (Claude Code)"}},
		"anthropic-api": fakeLLM{name: "anthropic-api"},
		"ollama":        fakeLLM{name: "ollama"},
	}, false)

	cli := got["claude-cli"]
	if !cli.Available || cli.CLI == nil || !cli.CLI.Installed || !cli.CLI.Authenticated || cli.CLI.Version != "2.1.282 (Claude Code)" {
		t.Fatalf("claude-cli = %+v (cli %+v)", cli, cli.CLI)
	}
	if !got["anthropic-api"].Available {
		t.Fatal("a statically configured API provider must be reported available")
	}
	if _, ok := got["ollama"]; ok {
		t.Fatal("ollama availability belongs to the residency manager, not this probe")
	}
}

func TestLLMProviderStatusReportsUnhealthyAndUnreachableCLI(t *testing.T) {
	unhealthy := llmProviderStatus(context.Background(), map[string]llm.Provider{
		"claude-cli": fakeCLI{status: claudecli.CLIStatus{Installed: true, Detail: "no token"}},
	}, false)["claude-cli"]
	if unhealthy.Available || unhealthy.CLI == nil || !unhealthy.CLI.Installed || unhealthy.CLI.Authenticated || unhealthy.DisabledReason == "" {
		t.Fatalf("unhealthy = %+v", unhealthy)
	}

	unreachable := llmProviderStatus(context.Background(), map[string]llm.Provider{
		"claude-cli": fakeCLI{err: errors.New("dial tcp: no such host")},
	}, false)["claude-cli"]
	if unreachable.Available || unreachable.CLI == nil || unreachable.CLI.Installed || unreachable.DisabledReason == "" {
		t.Fatalf("unreachable = %+v", unreachable)
	}
}

func TestLLMProviderStatusExplainsMissingCLIAdapter(t *testing.T) {
	got := llmProviderStatus(context.Background(), map[string]llm.Provider{}, false)["claude-cli"]
	if got.Available || got.DisabledReason == "" || got.CLI == nil {
		t.Fatalf("missing adapter = %+v", got)
	}
}
