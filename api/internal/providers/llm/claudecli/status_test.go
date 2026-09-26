package claudecli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"loomtale/api/internal/secretstr"
)

func TestStatusHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(versionHeader, "2.1.282 (Claude Code)")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(srv.URL, secretstr.String(""), srv.Client(), false)
	status, err := p.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.Authenticated || status.Detail != "" {
		t.Fatalf("status = %+v", status)
	}
	if status.Version != "2.1.282 (Claude Code)" {
		t.Fatalf("version = %q", status.Version)
	}
}

func TestStatusUnhealthyReportsDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("no oauth token configured"))
	}))
	defer srv.Close()

	p := New(srv.URL, secretstr.String(""), srv.Client(), false)
	status, err := p.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed {
		t.Fatal("expected installed=true (the sidecar answered)")
	}
	if status.Authenticated {
		t.Fatal("expected authenticated=false for a 503 response")
	}
	if status.Detail != "no oauth token configured" {
		t.Fatalf("detail = %q", status.Detail)
	}
}

func TestStatusUnreachableReturnsError(t *testing.T) {
	p := New("http://127.0.0.1:1", secretstr.String(""), http.DefaultClient, false)
	if _, err := p.Status(context.Background()); err == nil {
		t.Fatal("expected an error for an unreachable sidecar")
	}
}
