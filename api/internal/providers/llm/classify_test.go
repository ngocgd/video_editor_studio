package llm

import (
	"errors"
	"testing"

	"loomtale/api/internal/pipeline"
)

func TestClassifyHTTPMarksClientErrorsPermanent(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404} {
		err := ClassifyHTTP(code, "anthropic", "bad request")
		if !errors.Is(err, pipeline.ErrValidation) {
			t.Errorf("status %d: expected pipeline.ErrValidation, got %v", code, err)
		}
	}
}

func TestClassifyHTTPLeavesRateLimitAndServerErrorsTransient(t *testing.T) {
	for _, code := range []int{429, 500, 503, 529} {
		err := ClassifyHTTP(code, "anthropic", "overloaded")
		if errors.Is(err, pipeline.ErrValidation) {
			t.Errorf("status %d: expected a transient (unwrapped) error, got %v", code, err)
		}
		class, _ := pipeline.Classify(err)
		if class != pipeline.ClassTransient {
			t.Errorf("status %d: Classify() = %v, want ClassTransient", code, class)
		}
	}
}

func TestClassifyOllamaErrorMapsOOMToGPUOOM(t *testing.T) {
	err := ClassifyOllamaError(500, "CUDA error: out of memory")
	if !errors.Is(err, pipeline.ErrGPUOOM) {
		t.Fatalf("expected pipeline.ErrGPUOOM, got %v", err)
	}
	class, code := pipeline.Classify(err)
	if class != pipeline.ClassGPUOOM || code != "gpu_oom" {
		t.Fatalf("Classify() = %v/%q, want ClassGPUOOM/gpu_oom", class, code)
	}
}

func TestClassifyOllamaErrorFallsBackToHTTPClassification(t *testing.T) {
	err := ClassifyOllamaError(404, "model not found")
	if !errors.Is(err, pipeline.ErrValidation) {
		t.Fatalf("expected pipeline.ErrValidation, got %v", err)
	}
}
