package gemini

import (
	"context"
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
		for _, e := range events {
			_, _ = w.Write([]byte(e + "\n\n"))
		}
	}))
}

func TestGenerateAggregatesSSEDeltasAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-goog-api-key"); got != "gk-test" {
			t.Fatalf("x-goog-api-key = %q", got)
		}
		events := []string{
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hello "}]}}],"usageMetadata":{"promptTokenCount":7}}`,
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":2}}`,
		}
		for _, e := range events {
			_, _ = w.Write([]byte(e + "\n\n"))
		}
	}))
	defer server.Close()

	p := New(secretstr.String("gk-test"), "gemini-x", server.Client())
	p.BaseURL = server.URL
	resp, err := p.Generate(context.Background(), llm.Request{Messages: []llm.Message{{Role: "user", Text: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello world" {
		t.Fatalf("text = %q", resp.Text)
	}
	if resp.Usage.In != 7 || resp.Usage.Out != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestGenerateSurfacesNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	p := New(secretstr.String("gk-bad"), "gemini-x", server.Client())
	p.BaseURL = server.URL
	_, err := p.Generate(context.Background(), llm.Request{})
	if err == nil {
		t.Fatal("expected error on 403 response")
	}
	if !errors.Is(err, pipeline.ErrValidation) {
		t.Fatalf("expected a permanent (pipeline.ErrValidation) error for 403, got %v", err)
	}
}

func TestGenerateRejectsStreamWithoutFinishReason(t *testing.T) {
	server := newTestServer(t, []string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`,
	})
	defer server.Close()

	p := New(secretstr.String("gk-test"), "gemini-x", server.Client())
	p.BaseURL = server.URL
	_, err := p.Generate(context.Background(), llm.Request{})
	if !errors.Is(err, ErrStreamIncomplete) {
		t.Fatalf("expected ErrStreamIncomplete, got %v", err)
	}
}

func TestGenerateSurfacesMidStreamErrorField(t *testing.T) {
	server := newTestServer(t, []string{
		`data: {"error":{"code":500,"message":"internal","status":"INTERNAL"}}`,
	})
	defer server.Close()

	p := New(secretstr.String("gk-test"), "gemini-x", server.Client())
	p.BaseURL = server.URL
	if _, err := p.Generate(context.Background(), llm.Request{}); err == nil {
		t.Fatal("expected error on mid-stream error field")
	}
}

func TestGenerateSurfacesMaxTokensTruncation(t *testing.T) {
	server := newTestServer(t, []string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"cut"}]},"finishReason":"MAX_TOKENS"}]}`,
	})
	defer server.Close()

	p := New(secretstr.String("gk-test"), "gemini-x", server.Client())
	p.BaseURL = server.URL
	_, err := p.Generate(context.Background(), llm.Request{})
	if !errors.Is(err, ErrMaxTokensTruncated) {
		t.Fatalf("expected ErrMaxTokensTruncated, got %v", err)
	}
}

func TestGenerateSurfacesSafetyFiltering(t *testing.T) {
	server := newTestServer(t, []string{
		`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"SAFETY"}]}`,
	})
	defer server.Close()

	p := New(secretstr.String("gk-test"), "gemini-x", server.Client())
	p.BaseURL = server.URL
	_, err := p.Generate(context.Background(), llm.Request{})
	if !errors.Is(err, ErrContentFiltered) || !errors.Is(err, pipeline.ErrValidation) {
		t.Fatalf("expected ErrContentFiltered/pipeline.ErrValidation, got %v", err)
	}
}

func TestGenerateRejectsMalformedChunk(t *testing.T) {
	server := newTestServer(t, []string{`data: {not-json`})
	defer server.Close()

	p := New(secretstr.String("gk-test"), "gemini-x", server.Client())
	p.BaseURL = server.URL
	if _, err := p.Generate(context.Background(), llm.Request{}); err == nil {
		t.Fatal("expected error on malformed chunk instead of silently skipping it")
	}
}
