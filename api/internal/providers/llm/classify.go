package llm

import (
	"fmt"
	"strings"

	"loomtale/api/internal/pipeline"
)

// ClassifyHTTP maps an HTTP status code from a REST-based provider
// (anthropic, gemini) to the phase 3 pipeline sentinels, so a
// StepHandler.Run can hand the resulting error straight to
// pipeline.Classify. 429 and 5xx are left unwrapped since
// pipeline.Classify's default (transient) is already the right policy
// for rate limits and server errors; 400/401/403/404 indicate a
// request-shape, auth or missing-resource problem that will not fix
// itself on retry.
func ClassifyHTTP(statusCode int, provider, body string) error {
	err := fmt.Errorf("%s: unexpected status %d: %s", provider, statusCode, body)
	if statusCode == 429 || statusCode >= 500 {
		return err
	}
	if statusCode >= 400 {
		return fmt.Errorf("%w: %s", pipeline.ErrValidation, err)
	}
	return err
}

// ClassifyOllamaError inspects an Ollama error body for a CUDA
// out-of-memory indicator and maps it to pipeline.ErrGPUOOM (Ollama
// returns a plain 500 for an OOM, with no distinct status code), so the
// dispatcher's gpu_oom full-unload-and-retry policy runs for Ollama
// exactly as it does for the Python worker. Falls back to ClassifyHTTP
// for every other error shape.
func ClassifyOllamaError(statusCode int, body string) error {
	lower := strings.ToLower(body)
	if strings.Contains(lower, "out of memory") || strings.Contains(lower, "cuda error") {
		return fmt.Errorf("%w: ollama: %s", pipeline.ErrGPUOOM, body)
	}
	return ClassifyHTTP(statusCode, "ollama", body)
}
