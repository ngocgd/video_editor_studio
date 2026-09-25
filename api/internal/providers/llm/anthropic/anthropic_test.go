package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/secretstr"
)

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
			`data: {"type":"message_delta","usage":{"output_tokens":2}}`,
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
	if _, err := p.Generate(context.Background(), llm.Request{}); err == nil {
		t.Fatal("expected error on 401 response")
	}
}
