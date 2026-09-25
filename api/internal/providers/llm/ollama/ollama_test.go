package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"loomtale/api/internal/providers/llm"
)

func TestGenerateReturnsAggregatedText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"hello "},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"world"},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true,"eval_count":3,"prompt_eval_count":5}` + "\n"))
	}))
	defer server.Close()

	p := New(server.URL, "llama3", server.Client())
	resp, err := p.Generate(context.Background(), llm.Request{Messages: []llm.Message{{Role: "user", Text: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello world" {
		t.Fatalf("text = %q, want %q", resp.Text, "hello world")
	}
	if resp.Usage.In != 5 || resp.Usage.Out != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.Provider != "ollama" {
		t.Fatalf("provider = %q", resp.Provider)
	}
}

func TestStreamInvokesOnDeltaPerChunk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"a"},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"b"},"done":true}` + "\n"))
	}))
	defer server.Close()

	p := New(server.URL, "llama3", server.Client())
	var deltas []string
	_, err := p.Stream(context.Background(), llm.Request{}, func(d llm.Delta) { deltas = append(deltas, d.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "") != "ab" {
		t.Fatalf("deltas = %v", deltas)
	}
}

func TestGenerateSurfacesNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	p := New(server.URL, "llama3", server.Client())
	if _, err := p.Generate(context.Background(), llm.Request{}); err == nil {
		t.Fatal("expected error on 500 response")
	}
}

func TestLoadedReportsResidentModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3"}]}`))
	}))
	defer server.Close()

	p := New(server.URL, "llama3", server.Client())
	loaded, err := p.Loaded(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !loaded {
		t.Fatal("expected llama3 to be reported loaded")
	}
}
