package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// runRequestBody is the wire shape POST /v1/run accepts from the worker.
// The prompt travels only in this JSON body (over the internal llm_net,
// bearer-token authenticated), never as a CLI argv element.
type runRequestBody struct {
	Prompt string `json:"prompt"`
}

// streamLine is one NDJSON line of the response: either a delta or the
// final result, never both.
type streamLine struct {
	Type      string  `json:"type"` // "delta" | "result"
	Text      string  `json:"text,omitempty"`
	IsError   bool    `json:"is_error,omitempty"`
	Error     string  `json:"error,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
	InTokens  int     `json:"in_tokens,omitempty"`
	OutTokens int     `json:"out_tokens,omitempty"`
}

type handler struct {
	runner         *runner
	model          string
	systemPrompt   string
	oauthToken     string
	bearerToken    string
	disabledReason string
}

func (h *handler) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	if h.disabledReason != "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(h.disabledReason))
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleRun authenticates with a constant-time bearer comparison, then
// spawns one claude CLI invocation and streams NDJSON deltas as they
// arrive, followed by exactly one final "result" line.
func (h *handler) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.disabledReason != "" {
		http.Error(w, "llm-cli provider disabled: "+h.disabledReason, http.StatusServiceUnavailable)
		return
	}

	var body runRequestBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Prompt) == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	// Commit the 200 status and flush the headers before the CLI runs.
	// Without partial messages the CLI prints its first stream-json line
	// only when the whole reply is done, so waiting for that line would
	// hold the headers back for the full generation and trip the
	// caller's response-header timeout. Every failure after this point
	// travels in-band as the final "result" line.
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	result, err := h.runner.Run(r.Context(), runRequest{
		Model:        h.model,
		SystemPrompt: h.systemPrompt,
		Prompt:       body.Prompt,
		OAuthToken:   h.oauthToken,
	}, func(delta string) {
		writeJSONLine(w, flusher, streamLine{Type: "delta", Text: delta})
	})

	final := streamLine{Type: "result"}
	switch {
	case errors.Is(err, ErrToolsEnabled):
		final.IsError = true
		final.Error = "cli_tools_enabled"
	case err != nil:
		final.IsError = true
		final.Error = err.Error()
	case result.IsError:
		final.IsError = true
		final.Error = result.ErrorText
	default:
		final.Text = result.Text
		final.CostUSD = result.TotalCostUSD
		final.InTokens = result.InputTokens
		final.OutTokens = result.OutputTokens
	}
	writeJSONLine(w, flusher, final)
}

func (h *handler) authorized(r *http.Request) bool {
	if h.bearerToken == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	got := strings.TrimPrefix(auth, prefix)
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.bearerToken)) == 1
}
