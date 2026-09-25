package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/secretstr"
)

func newTestServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			_, _ = w.Write([]byte(e + "\n\n"))
		}
	}))
}

func TestGenerateAggregatesSSEDeltasAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "sk-test" {
			t.Fatalf("x-api-key = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello "}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"world"}}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			`data: {"type":"message_stop"}`,
		}
		for _, e := range events {
			_, _ = w.Write([]byte(e + "\n\n"))
		}
	}))
	defer server.Close()

	p := New(secretstr.String("sk-test"), "claude-x", server.Client())
	p.BaseURL = server.URL
	resp, err := p.Generate(context.Background(), llm.Request{Messages: []llm.Message{{Role: "user", Text: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello world" {
		t.Fatalf("text = %q", resp.Text)
	}
	if resp.Usage.In != 10 || resp.Usage.Out != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestGenerateSurfacesNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer server.Close()

	p := New(secretstr.String("sk-bad"), "claude-x", server.Client())
	p.BaseURL = server.URL
	_, err := p.Generate(context.Background(), llm.Request{})
	if err == nil {
		t.Fatal("expected error on 401 response")
	}
	if !errors.Is(err, pipeline.ErrValidation) {
		t.Fatalf("expected a permanent (pipeline.ErrValidation) error for 401, got %v", err)
	}
}

func TestGenerateRejectsStreamWithoutMessageStop(t *testing.T) {
	server := newTestServer(t, []string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"partial"}}`,
	})
	defer server.Close()

	p := New(secretstr.String("sk-test"), "claude-x", server.Client())
	p.BaseURL = server.URL
	_, err := p.Generate(context.Background(), llm.Request{})
	if !errors.Is(err, ErrStreamIncomplete) {
		t.Fatalf("expected ErrStreamIncomplete, got %v", err)
	}
}

func TestGenerateSurfacesMidStreamErrorEvent(t *testing.T) {
	server := newTestServer(t, []string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`,
	})
	defer server.Close()

	p := New(secretstr.String("sk-test"), "claude-x", server.Client())
	p.BaseURL = server.URL
	if _, err := p.Generate(context.Background(), llm.Request{}); err == nil {
		t.Fatal("expected error on mid-stream error event")
	}
}

func TestGenerateSurfacesMaxTokensTruncation(t *testing.T) {
	server := newTestServer(t, []string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"cut off"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":1}}`,
		`data: {"type":"message_stop"}`,
	})
	defer server.Close()

	p := New(secretstr.String("sk-test"), "claude-x", server.Client())
	p.BaseURL = server.URL
	_, err := p.Generate(context.Background(), llm.Request{})
	if !errors.Is(err, ErrMaxTokensTruncated) {
		t.Fatalf("expected ErrMaxTokensTruncated, got %v", err)
	}
}

func TestGenerateSurfacesRefusal(t *testing.T) {
	server := newTestServer(t, []string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":0}}`,
		`data: {"type":"message_stop"}`,
	})
	defer server.Close()

	p := New(secretstr.String("sk-test"), "claude-x", server.Client())
	p.BaseURL = server.URL
	_, err := p.Generate(context.Background(), llm.Request{})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("expected ErrRefused, got %v", err)
	}
	if !errors.Is(err, pipeline.ErrValidation) {
		t.Fatalf("expected ErrRefused to classify as permanent, got %v", err)
	}
}

func TestGenerateSendsSystemPrompt(t *testing.T) {
	var gotSystem string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			System string `json:"system"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotSystem = body.System
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"message_stop"}` + "\n\n"))
	}))
	defer server.Close()

	p := New(secretstr.String("sk-test"), "claude-x", server.Client())
	p.BaseURL = server.URL
	_, _ = p.Generate(context.Background(), llm.Request{System: "You are an outline writer."})
	if gotSystem != "You are an outline writer." {
		t.Fatalf("system = %q", gotSystem)
	}
}
