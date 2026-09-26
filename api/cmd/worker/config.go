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
	OllamaURL         string `env:"OLLAMA_URL" envDefault:"http://ollama:11434"`
	OllamaModel       string `env:"OLLAMA_MODEL" envDefault:""`
	ComfyUIURL        string `env:"COMFYUI_URL" envDefault:"http://comfyui:8188"`
	PyworkerAddr      string `env:"PYWORKER_ADDR" envDefault:"pyworker:9090"`
	PyworkerTokenPath string `env:"PYWORKER_BEARER_TOKEN_PATH" envDefault:""`

	// AllowedProviderHosts/LLMCLI*/Anthropic*/Gemini* mirror cmd/api's own
	// env vars exactly (same names, same defaults) so bootstrap.Build
	// constructs the identical set of LLM adapters in both processes:
	// the API enqueues llm.* steps, this worker is what actually runs
	// them, so it needs the same provider adapters, not an empty
	// registry.
	AppMode              string   `env:"APP_MODE" envDefault:"local"`
	AllowedProviderHosts []string `env:"ALLOWED_PROVIDER_HOSTS" envSeparator:"," envDefault:"ollama"`

	LLMCLIURL             string `env:"LLMCLI_URL" envDefault:"http://llm-cli:8090"`
	LLMCLIBearerTokenPath string `env:"LLMCLI_BEARER_TOKEN_PATH" envDefault:""`

	AnthropicAPIKeyPath string `env:"ANTHROPIC_API_KEY_PATH" envDefault:""`
	AnthropicModel      string `env:"ANTHROPIC_MODEL" envDefault:"claude-sonnet-5"`
	GeminiAPIKeyPath    string `env:"GEMINI_API_KEY_PATH" envDefault:""`
	GeminiModel         string `env:"GEMINI_MODEL" envDefault:"gemini-2.5-flash"`

	// MasterKeyPath must match cmd/api's mounted envelope-encryption KEK
	// so this worker can open the SAME tenants' BYOK secrets (the KEK is
	// process-config, not per-process-generated).
	MasterKeyPath string `env:"MASTER_KEY_PATH" envDefault:"/run/secrets/master_key"`

	// ModelsDir is the models volume, mounted read-write only into this
	// worker (compose.gpu.yml). Empty disables the models.pull step:
	// the base stack has no models volume to download into.
	ModelsDir string `env:"MODELS_DIR" envDefault:""`
	// ModelsHostDiskDir is a read-only bind mount from the host drive
	// that holds the Docker data disk; the download pre-flight reads the
	// host's real free space there (see models.Downloader.HostDiskDir).
	// Empty refuses every download.
	ModelsHostDiskDir string `env:"MODELS_HOST_DISK_DIR" envDefault:""`

	// PinCharacters mirrors cmd/api's STORY_PIN_CHARACTERS: the worker is
	// what actually builds the LLM requests.
	PinCharacters bool `env:"STORY_PIN_CHARACTERS" envDefault:"true"`

	// FFmpegPath is the ffmpeg binary the media and voice steps run.
	FFmpegPath string `env:"FFMPEG_PATH" envDefault:"ffmpeg"`
	// FFprobePath is the ffprobe binary the render QC and keyframe
	// checks run.
	FFprobePath string `env:"FFPROBE_PATH" envDefault:"ffprobe"`

	// DiskGuardPath and the thresholds mirror cmd/api's: the worker also
	// enqueues renders (a scene edit restarts a running render).
	DiskGuardPath  string `env:"DISK_GUARD_PATH" envDefault:""`
	DiskMinFreeGB  uint64 `env:"DISK_MIN_FREE_GB" envDefault:"40"`
	DiskWarnFreeGB uint64 `env:"DISK_WARN_FREE_GB" envDefault:"60"`
}
