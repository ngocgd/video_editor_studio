package main

import (
	"context"
	"net/http"
	"os"
	"strings"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/image/comfyui"
	"loomtale/api/internal/providers/llm/ollama"
	"loomtale/api/internal/providers/pyworker"
	"loomtale/api/internal/providers/residency"
	"loomtale/api/internal/providers/workerconn"
	"loomtale/api/internal/secretstr"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// buildResidency constructs the real residency manager when WORKER_GPU is
// true: this process joins gpu_net (see compose.gpu.yml) and so is the
// only one able to reach comfyui/ollama/pyworker directly, unlike cmd/api.
func buildResidency(ctx context.Context, cfg config) (pipeline.ModelResidency, pipeline.GpuProbe, error) {
	if !cfg.WorkerGPU {
		return pipeline.NoopResidency{}, nil, nil
	}

	ollamaProvider := ollama.New(cfg.OllamaURL, cfg.OllamaModel, http.DefaultClient)
	comfyClient := comfyui.New(cfg.ComfyUIURL, http.DefaultClient)

	backends := map[string]residency.Backend{
		"ollama":  &ollama.Backend{Provider: ollamaProvider},
		"comfyui": &comfyui.Backend{Client: comfyClient},
	}
	if token := readFileOrEmpty(cfg.PyworkerTokenPath); token != "" {
		conn, err := workerconn.Dial(cfg.PyworkerAddr, secretstr.String(token))
		if err != nil {
			return nil, nil, err
		}
		pw := pyworker.New(workerv1.NewWorkerClient(conn))
		// Engine slot names are assigned as real StepHandlers register
		// them in phases 6+; none exist yet, so no pyworker backend is
		// added to the map until then.
		_ = pw
	}

	probe := &residency.NvidiaSMIProbe{RenderReserveMB: cfg.RenderReserveMB}
	manager, err := residency.NewManagerWithBudget(ctx, probe, backends, nil, cfg.RenderReserveMB)
	if err != nil {
		return nil, nil, err
	}
	return manager, manager, nil
}

func readFileOrEmpty(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
