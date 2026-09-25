package gemini

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
		if got := r.Header.Get("x-goog-api-key"); got != "gk-test" {
			t.Fatalf("x-goog-api-key = %q", got)
		}
		events := []string{
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hello "}]}}],"usageMetadata":{"promptTokenCount":7}}`,
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"world"}]}}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":2}}`,
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
	if _, err := p.Generate(context.Background(), llm.Request{}); err == nil {
		t.Fatal("expected error on 403 response")
	}
}
