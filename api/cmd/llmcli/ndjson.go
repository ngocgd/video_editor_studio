package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// maxNDJSONLine caps a single stream-json line (the phase contract: 4MB
// line cap, 2MB total output cap).
const maxNDJSONLine = 4 * 1024 * 1024

// maxTotalOutput caps the sum of all assistant text this parser will
// accumulate before refusing to read further.
const maxTotalOutput = 2 * 1024 * 1024

// ErrToolsEnabled is returned when the system/init event reports any tool
// or MCP server, meaning the CLI did not honor --tools ""/--mcp-config:
// this is treated as a permanent, non-retryable failure.
var ErrToolsEnabled = errors.New("cli_tools_enabled")

// ErrOutputTooLarge is returned when the accumulated assistant text
// exceeds maxTotalOutput.
var ErrOutputTooLarge = errors.New("llmcli: streamed output exceeded the total size cap")

// ErrEventBeforeInit is returned when an assistant or result event
// arrives before system/init has confirmed tools/mcp_servers are empty:
// relaying any text to the caller before that assertion runs would let
// output from a not-yet-verified process reach SSE.
var ErrEventBeforeInit = errors.New("llmcli: assistant/result event received before system/init")

// initEvent is the system/init event's shape.
type initEvent struct {
	Type       string   `json:"type"`
	Subtype    string   `json:"subtype"`
	Tools      []string `json:"tools"`
	MCPServers []any    `json:"mcp_servers"`
}

// contentBlock is one block of an assistant message's content array.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type assistantMessage struct {
	Content []contentBlock `json:"content"`
}

type assistantEvent struct {
	Type    string           `json:"type"`
	Message assistantMessage `json:"message"`
}

// resultEvent is the CLI's final stream-json event.
type resultEvent struct {
	Type         string  `json:"type"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// parseResult is the fully parsed outcome of one CLI invocation.
type parseResult struct {
	InitOK       bool
	Text         string
	IsError      bool
	ErrorText    string
	TotalCostUSD float64
	InputTokens  int
	OutputTokens int
}

// parseNDJSONStream reads the claude CLI's stream-json output line by
// line, invoking onDelta for every assistant text delta as it arrives.
// It returns ErrToolsEnabled immediately (without waiting for the process
// to exit) the moment system/init reports a non-empty tools or
// mcp_servers list, so the caller can kill the process right away.
func parseNDJSONStream(r io.Reader, onDelta func(string)) (parseResult, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxNDJSONLine)

	var res parseResult
	var totalOut int

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			return res, fmt.Errorf("llmcli: decode stream-json line: %w", err)
		}

		switch probe.Type {
		case "system":
			var ev initEvent
			if err := json.Unmarshal(line, &ev); err != nil {
				return res, fmt.Errorf("llmcli: decode init event: %w", err)
			}
			if ev.Subtype == "init" {
				if len(ev.Tools) > 0 || len(ev.MCPServers) > 0 {
					return res, ErrToolsEnabled
				}
				res.InitOK = true
			}
		case "assistant":
			if !res.InitOK {
				return res, ErrEventBeforeInit
			}
			var ev assistantEvent
			if err := json.Unmarshal(line, &ev); err != nil {
				return res, fmt.Errorf("llmcli: decode assistant event: %w", err)
			}
			for _, block := range ev.Message.Content {
				if block.Type != "text" || block.Text == "" {
					continue
				}
				totalOut += len(block.Text)
				if totalOut > maxTotalOutput {
					return res, ErrOutputTooLarge
				}
				res.Text += block.Text
				if onDelta != nil {
					onDelta(block.Text)
				}
			}
		case "result":
			if !res.InitOK {
				return res, ErrEventBeforeInit
			}
			var ev resultEvent
			if err := json.Unmarshal(line, &ev); err != nil {
				return res, fmt.Errorf("llmcli: decode result event: %w", err)
			}
			res.IsError = ev.IsError
			res.TotalCostUSD = ev.TotalCostUSD
			res.InputTokens = ev.Usage.InputTokens
			res.OutputTokens = ev.Usage.OutputTokens
			if ev.IsError {
				res.ErrorText = ev.Result
			} else if res.Text == "" {
				// The result event carries the final text even for CLI
				// versions that omit incremental assistant deltas.
				res.Text = ev.Result
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return res, fmt.Errorf("llmcli: read stream: %w", err)
	}
	return res, nil
}
