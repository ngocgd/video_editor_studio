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
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(srv.URL, secretstr.String(""), srv.Client(), false)
	installed, authenticated, detail, err := p.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !installed || !authenticated || detail != "" {
		t.Fatalf("installed=%v authenticated=%v detail=%q", installed, authenticated, detail)
	}
}

func TestStatusUnhealthyReportsDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("no oauth token configured"))
	}))
	defer srv.Close()

	p := New(srv.URL, secretstr.String(""), srv.Client(), false)
	installed, authenticated, detail, err := p.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("expected installed=true (the sidecar answered)")
	}
	if authenticated {
		t.Fatal("expected authenticated=false for a 503 response")
	}
	if detail != "no oauth token configured" {
		t.Fatalf("detail = %q", detail)
	}
}

func TestStatusUnreachableReturnsError(t *testing.T) {
	p := New("http://127.0.0.1:1", secretstr.String(""), http.DefaultClient, false)
	_, _, _, err := p.Status(context.Background())
	if err == nil {
		t.Fatal("expected an error for an unreachable sidecar")
	}
}
