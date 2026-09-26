package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"loomtale/api/internal/models"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/image/comfyui"
	"loomtale/api/internal/providers/llm/ollama"
	"loomtale/api/internal/providers/pyworker"
	"loomtale/api/internal/providers/residency"
	"loomtale/api/internal/providers/workerconn"
	"loomtale/api/internal/secretstr"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// backendDialTimeout/backendResponseHeaderTimeout bound a hung
// connection to ollama/comfyui without cutting off a legitimately long
// generation once it starts responding (no overall Client.Timeout).
const (
	backendDialTimeout           = 5 * time.Second
	backendResponseHeaderTimeout = 15 * time.Second
)

func backendHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: backendDialTimeout}
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			ResponseHeaderTimeout: backendResponseHeaderTimeout,
		},
	}
}

// buildResidency constructs the real residency manager when WORKER_GPU is
// true: this process joins gpu_net (see compose.gpu.yml) and so is the
// only one able to reach comfyui/ollama/pyworker directly, unlike cmd/api.
// The concrete *residency.Manager is also returned (nil when
// WORKER_GPU=false) so main.go can wire OnChange for the worker_status
// heartbeat without a type assertion.
func buildResidency(ctx context.Context, cfg config, manifest *models.Manifest, gate func(context.Context, string) error) (pipeline.ModelResidency, pipeline.GpuProbe, *residency.Manager, error) {
	if !cfg.WorkerGPU {
		return pipeline.NoopResidency{}, nil, nil, nil
	}

	ollamaProvider := ollama.New(cfg.OllamaURL, cfg.OllamaModel, backendHTTPClient())
	templates, err := models.EmbeddedTemplates()
	if err != nil {
		return nil, nil, nil, err
	}
	comfyEngine := &comfyui.Engine{Client: comfyui.New(cfg.ComfyUIURL, backendHTTPClient()), Templates: templates}

	backends := map[string]residency.Backend{
		"ollama": &ollama.Backend{Provider: ollamaProvider},
		// Every ComfyUI model is loaded through its warm-up workflow,
		// after the licence and installed-files gate.
		"comfyui": &comfyui.Backend{Engine: comfyEngine, Warmups: manifest.Warmups(), Gate: gate},
	}

	// VRAM is read through pyworker's GpuStatus RPC (pynvml-backed), not
	// by shelling out to nvidia-smi from this process: the distroless
	// runtime image this binary ships in has no /usr/bin for
	// nvidia-container-toolkit to inject nvidia-smi into (verified live —
	// pyworker's Debian-based image gets the binary, this one does not),
	// so residency.NvidiaSMIProbe only ever works as a fallback for a
	// deployment that somehow runs this binary from a non-distroless
	// image. NewManagerWithBudget's own "no_gpu handled" degrades safely
	// either way.
	var probe pipeline.GpuProbe = &residency.NvidiaSMIProbe{RenderReserveMB: cfg.RenderReserveMB}
	if token := readFileOrEmpty(cfg.PyworkerTokenPath); token != "" {
		conn, err := workerconn.Dial(cfg.PyworkerAddr, secretstr.String(token))
		if err != nil {
			return nil, nil, nil, err
		}
		pw := pyworker.New(workerv1.NewWorkerClient(conn))
		// A single generic "pyworker" slot (empty engine name): the
		// worker.proto contract reports and unloads whichever one engine
		// is resident at a time, so UnloadAll can release Python-side
		// VRAM on an Ollama switch or a gpu_oom unload without needing
		// per-engine backend entries — phases 9a-9c add those once real
		// engines and their own ModelRef.Backend values exist.
		backends["pyworker"] = &pyworker.Backend{Client: pw, Engine: ""}
		probe = &pyworker.Probe{Client: pw, RenderReserveMB: cfg.RenderReserveMB}
	}

	// The manifest's VRAM ceilings make Ensure wait until that much VRAM
	// is actually free after an unload before loading the next model.
	manager, err := residency.NewManagerWithBudget(ctx, probe, backends, manifest.VRAMByRef(), cfg.RenderReserveMB)
	if err != nil {
		return nil, nil, nil, err
	}
	return manager, manager, manager, nil
}

// downloadHTTPClient bounds connection setup for model downloads but
// sets no overall timeout: a 13 GB file legitimately streams for many
// minutes, and the pull step's own job timeout bounds it.
func downloadHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		},
	}
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
