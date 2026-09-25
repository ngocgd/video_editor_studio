package claudecli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/secretstr"
)

func TestGenerateRelaysSidecarStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sidecar-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"type":"delta","text":"hi "}` + "\n"))
		_, _ = w.Write([]byte(`{"type":"delta","text":"there"}` + "\n"))
		_, _ = w.Write([]byte(`{"type":"result","text":"hi there","cost_usd":0.001,"in_tokens":5,"out_tokens":2}` + "\n"))
	}))
	defer server.Close()

	p := New(server.URL, secretstr.String("sidecar-secret"), server.Client(), false)
	var deltas []string
	resp, err := p.Stream(context.Background(), llm.Request{Messages: []llm.Message{{Role: "user", Text: "hi"}}}, func(d llm.Delta) {
		deltas = append(deltas, d.Text)
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "") != "hi there" {
		t.Fatalf("deltas = %v", deltas)
	}
	if resp.Text != "hi there" || resp.Usage.In != 5 || resp.Usage.Out != 2 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestGenerateSurfacesSidecarError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"result","is_error":true,"error":"cli_tools_enabled"}` + "\n"))
	}))
	defer server.Close()

	p := New(server.URL, secretstr.String("sidecar-secret"), server.Client(), false)
	if _, err := p.Generate(context.Background(), llm.Request{}); err == nil {
		t.Fatal("expected error from sidecar result")
	}
}

func TestDisabledInSaaSMode(t *testing.T) {
	p := New("http://llm-cli:8090", secretstr.String("x"), http.DefaultClient, true)
	if _, err := p.Generate(context.Background(), llm.Request{}); err == nil {
		t.Fatal("expected claude-cli to refuse to run in SaaS mode")
	}
}
