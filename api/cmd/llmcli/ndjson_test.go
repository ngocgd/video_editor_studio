package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParseNDJSONStreamHappyPath(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","tools":[],"mcp_servers":[]}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello "}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"world"}]}}`,
		`{"type":"result","is_error":false,"result":"hello world","total_cost_usd":0.002,"usage":{"input_tokens":10,"output_tokens":2}}`,
	}, "\n")

	var deltas []string
	res, err := parseNDJSONStream(strings.NewReader(stream), func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	if !res.InitOK {
		t.Fatal("expected InitOK")
	}
	if res.Text != "hello world" {
		t.Fatalf("text = %q", res.Text)
	}
	if strings.Join(deltas, "") != "hello world" {
		t.Fatalf("deltas = %v", deltas)
	}
	if res.TotalCostUSD != 0.002 || res.InputTokens != 10 || res.OutputTokens != 2 {
		t.Fatalf("unexpected usage: %+v", res)
	}
}

func TestParseNDJSONStreamRejectsToolsEnabled(t *testing.T) {
	stream := `{"type":"system","subtype":"init","tools":["bash"],"mcp_servers":[]}`
	_, err := parseNDJSONStream(strings.NewReader(stream), nil)
	if !errors.Is(err, ErrToolsEnabled) {
		t.Fatalf("expected ErrToolsEnabled, got %v", err)
	}
}

func TestParseNDJSONStreamRejectsMCPServersEnabled(t *testing.T) {
	stream := `{"type":"system","subtype":"init","tools":[],"mcp_servers":[{"name":"evil"}]}`
	_, err := parseNDJSONStream(strings.NewReader(stream), nil)
	if !errors.Is(err, ErrToolsEnabled) {
		t.Fatalf("expected ErrToolsEnabled, got %v", err)
	}
}

func TestParseNDJSONStreamPropagatesIsError(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","tools":[],"mcp_servers":[]}`,
		`{"type":"result","is_error":true,"result":"rate limited"}`,
	}, "\n")
	res, err := parseNDJSONStream(strings.NewReader(stream), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.ErrorText != "rate limited" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestParseNDJSONStreamFallsBackToResultTextWhenNoDeltas(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","tools":[],"mcp_servers":[]}`,
		`{"type":"result","is_error":false,"result":"final only"}`,
	}, "\n")
	res, err := parseNDJSONStream(strings.NewReader(stream), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "final only" {
		t.Fatalf("text = %q", res.Text)
	}
}

func TestParseNDJSONStreamRejectsAssistantEventBeforeInit(t *testing.T) {
	stream := `{"type":"assistant","message":{"content":[{"type":"text","text":"early"}]}}`
	_, err := parseNDJSONStream(strings.NewReader(stream), nil)
	if !errors.Is(err, ErrEventBeforeInit) {
		t.Fatalf("expected ErrEventBeforeInit, got %v", err)
	}
}

func TestParseNDJSONStreamRejectsResultEventBeforeInit(t *testing.T) {
	stream := `{"type":"result","is_error":false,"result":"too early"}`
	_, err := parseNDJSONStream(strings.NewReader(stream), nil)
	if !errors.Is(err, ErrEventBeforeInit) {
		t.Fatalf("expected ErrEventBeforeInit, got %v", err)
	}
}
