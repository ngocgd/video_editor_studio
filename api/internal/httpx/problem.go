// Package httpx holds shared HTTP helpers: JSON responses, RFC 9457
// problem+json errors, and request-id propagation.
package httpx

import (
	"encoding/json"
	"net/http"
)

// Problem is an RFC 9457 problem+json body.
type Problem struct {
	Type     string `json:"type,omitempty"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

const problemContentType = "application/problem+json"

// WriteProblem writes p as a problem+json response with the given status.
func WriteProblem(w http.ResponseWriter, p Problem) {
	if p.Status == 0 {
		p.Status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// WriteJSON writes v as application/json with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
