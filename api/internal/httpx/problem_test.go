package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteProblemSetsContentTypeAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteProblem(rec, Problem{Title: "bad request", Status: http.StatusBadRequest, Detail: "missing field"})

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}

	var got Problem
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Title != "bad request" || got.Detail != "missing field" {
		t.Errorf("body = %+v", got)
	}
}

func TestWriteProblemDefaultsStatusTo500(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteProblem(rec, Problem{Title: "unexpected"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
