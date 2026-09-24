package health

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"loomtale/api/internal/httpapi/gen"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestGetHealthzReturnsOKAndVersion(t *testing.T) {
	h := &Handler{Version: "test-version"}

	resp, err := h.GetHealthz(context.Background(), gen.GetHealthzRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := resp.(gen.GetHealthz200JSONResponse)
	if !ok {
		t.Fatalf("expected GetHealthz200JSONResponse, got %T", resp)
	}
	if got.Version != "test-version" {
		t.Errorf("version = %q, want %q", got.Version, "test-version")
	}
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
}

func TestGetReadyzAllHealthyReturns200(t *testing.T) {
	h := &Handler{DB: fakePinger{}, Storage: fakePinger{}}

	resp, err := h.GetReadyz(context.Background(), gen.GetReadyzRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := resp.(gen.GetReadyz200JSONResponse)
	if !ok {
		t.Fatalf("expected GetReadyz200JSONResponse, got %T", resp)
	}
	if got.Checks == nil {
		t.Fatal("expected checks to be populated")
	}
	if (*got.Checks)["database"] != "ok" || (*got.Checks)["storage"] != "ok" {
		t.Errorf("checks = %+v, want both ok", *got.Checks)
	}
}

func TestGetReadyzDependencyDownReturns503(t *testing.T) {
	h := &Handler{DB: fakePinger{err: errors.New("connection refused")}, Storage: fakePinger{}}

	resp, err := h.GetReadyz(context.Background(), gen.GetReadyzRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := resp.(gen.GetReadyz503ApplicationProblemPlusJSONResponse)
	if !ok {
		t.Fatalf("expected GetReadyz503ApplicationProblemPlusJSONResponse, got %T", resp)
	}
	if got.Status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", got.Status, http.StatusServiceUnavailable)
	}
}
