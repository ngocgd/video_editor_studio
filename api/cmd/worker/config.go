package main

import "time"

// config holds the worker process settings, loaded via caarlos0/env.
type config struct {
	LogLevel        string        `env:"API_LOG_LEVEL" envDefault:"info"`
	DatabaseURL     string        `env:"DATABASE_URL,required"`
	ShutdownTimeout time.Duration `env:"WORKER_SHUTDOWN_TIMEOUT" envDefault:"30s"`

	// WORKER_GPU defaults to false in code, as the phase 3 contract
	// requires: phase 4 sets it to true only on the worker service in
	// compose.gpu.yml. With it false, the gpu queue is never registered
	// and NoopResidency is wired in, so a gpu-queue step can never
	// reach GPUExecutor.Run.
	WorkerGPU bool `env:"WORKER_GPU" envDefault:"false"`

	// RenderReserveMB is the minimum free VRAM a render.* job requires
	// before it is admitted (see pipeline.RenderAdmitted).
	RenderReserveMB int64 `env:"RENDER_RESERVE_MB" envDefault:"1024"`

	// CPUWorkers/LLMWorkers/RenderWorkers/IOWorkers size each queue's
	// concurrency. CPUWorkers defaults to 10 (cores-2 on the 12-vCPU
	// deployment VM, per the phase 3 requirement); the others are fixed,
	// low-concurrency queues that do not scale with core count.
	CPUWorkers    int `env:"WORKER_CPU_CONCURRENCY" envDefault:"10"`
	LLMWorkers    int `env:"WORKER_LLM_CONCURRENCY" envDefault:"5"`
	RenderWorkers int `env:"WORKER_RENDER_CONCURRENCY" envDefault:"2"`
	IOWorkers     int `env:"WORKER_IO_CONCURRENCY" envDefault:"5"`

	// The residency manager's backends. Only reachable when WorkerGPU is
	// true (this process then joins gpu_net alongside them, see
	// compose.gpu.yml); OllamaModel stays empty until phase 9b seeds a
	// model, in which case Ensure is simply never called for an
	// ollama-backed step (registry.ModelRefFor returns nil).
	OllamaURL          string `env:"OLLAMA_URL" envDefault:"http://ollama:11434"`
	OllamaModel        string `env:"OLLAMA_MODEL" envDefault:""`
	ComfyUIURL         string `env:"COMFYUI_URL" envDefault:"http://comfyui:8188"`
	PyworkerAddr       string `env:"PYWORKER_ADDR" envDefault:"pyworker:9090"`
	PyworkerTokenPath  string `env:"PYWORKER_BEARER_TOKEN_PATH" envDefault:""`

	// ModelsDir is the models volume, mounted read-write only into this
	// worker (compose.gpu.yml). Empty disables the models.pull step:
	// the base stack has no models volume to download into.
	ModelsDir string `env:"MODELS_DIR" envDefault:""`
}
