# Tech Stack — Loomtale Studio

Approved on 2026-09-24. Rationale and alternatives live in
[the brainstorm contract](../plans/reports/brainstorm-260924-2128-story-video-studio-contract.md)
and the linked research reports.

## Runtime shape

Internal single-user tool running on one Windows 11 PC (RTX 5060 Ti 16GB, 32GB RAM) via Docker Compose on WSL2, designed so it can become multi-tenant SaaS without a rewrite.

## Backend (Go)

| Concern | Choice |
|---|---|
| Language | Go |
| HTTP | chi router on `net/http` |
| Database | PostgreSQL, `pgx` driver, `sqlc` for type-safe queries |
| Migrations | goose |
| Jobs / pipeline orchestration | River (Postgres-backed). Queue `gpu` has concurrency 1; queue `cpu` runs in parallel. Each pipeline step is its own job; step state lives in Postgres so any step can be retried or resumed. |
| Progress to UI | Server-Sent Events |
| Object storage | MinIO (S3 API); swap to R2/S3 by config |
| Auth | Session auth from day one; every business table has `tenant_id` |

## AI workers

| Concern | Choice |
|---|---|
| Worker language | Python services in their own containers |
| Go ↔ Python contract | gRPC with streamed progress (`proto/`) |
| Image generation | ComfyUI via its HTTP + WebSocket API |
| LLM providers (one interface) | Ollama (local default), `claude` CLI subprocess (local), Anthropic API key, Gemini API key (SaaS) |
| TTS | Local TTS worker (EN and VI engines selected after benchmark) |
| Subtitles | faster-whisper / WhisperX alignment worker |
| Video assembly | FFmpeg with NVENC, orchestrated from Go |
| YouTube | Official YouTube Data API v3 + Analytics API, OAuth per channel |

Candidate models (Gemma 4 12B / Qwen3.5-9B, Z-Image Turbo, Qwen-Image, Chatterbox, VieNeu-TTS, …) are chosen in the benchmark phase. Every model must have a license that allows monetized use.

## Frontend

React + Vite, TanStack Router + TanStack Query, shadcn/ui (Radix + Tailwind). A single-page app, with no SSR. Design rules: [design-guidelines.md](design-guidelines.md).

## Infrastructure

- `deploy/compose.yml`: API, worker, Postgres, MinIO, web.
- `deploy/compose.gpu.yml`: ComfyUI, the Python GPU worker and the `llm-cli` sidecar (NVIDIA Container Toolkit).
- `deploy/compose.tools.yml`: toolbox container for Go, codegen and tests (the host has no Go toolchain).
- WSL2 VM limits: `C:\Users\ADMIN\.wslconfig` set to 20GB RAM, 12 CPUs, 16GB swap on 2026-09-24.
- GPU passthrough verified on 2026-09-24: `nvidia/cuda:12.8` container sees the RTX 5060 Ti.

## Explicit exclusions

Background music, affiliate features, billing, full-motion AI video for whole episodes, captcha or browser-automation uploads.
