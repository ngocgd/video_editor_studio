# Phase 04: Provider layer (LLM, image, TTS, align, train, vision), gRPC contracts, Python worker, ComfyUI client, llm-cli sidecar

## Context links
- [plan.md](plan.md) · [contract §6, §10 (Claude CLI pattern), §11 security](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- [backend research §1 (CLI pattern, ToS), §3 (gRPC vs ComfyUI HTTP+WS)](../reports/researcher-260924-2128-backend-architecture-goclaw.md)
- Pattern reference only (CC BY-NC, **do not copy code**) at `C:/Users/ADMIN/goclaw/internal/providers/`: `claude_cli_session.go:123` `buildArgs`, `claude_cli_session.go:417` `filterCLIEnv`, `claude_cli_chat.go:136-139` (`exec.CommandContext`, `WaitDelay`, `cmd.Dir`, filtered env), `claude_cli.go:189` (per-session mutex).
- Host CLI verified 2026-09-24: `claude --help` lists `--tools ""` (disables all tools), `--setting-sources`, `--strict-mcp-config`, `--system-prompt`, `--no-session-persistence`, `--disable-slash-commands`. `--bare` skips OAuth, so it cannot be used with a subscription.
- [phase 1b spike record](phase-01b-blackwell-comfyui-smoke-spike.md) gives the measured free-VRAM baseline.

## Overview
- Priority: P1 · Status: pending · Effort: 24h <!-- RT#15 re-estimate -->
- This phase delivers one provider interface per modality, config-driven selection (AC8), a claude CLI adapter isolated in its own `llm-cli` sidecar, a VRAM-aware model residency manager (the phase 3 pact), all gRPC contracts (TTS, align, train, vision), network isolation for GPU containers, and a Python worker that runs today without any model. No model is downloaded here; engines arrive in phases 9a–9c.

## Requirements
- **LLM interface** (`api/internal/providers/llm`): `Generate(ctx, Request) (Response, error)` and `Stream(ctx, Request, func(Delta)) (Response, error)`.
  - `Request{System, Messages, Data []DataBlock, MaxTokens, Temperature, JSONSchema}` where `DataBlock{Label, Text, Origin (user|import|derived|model), Tainted bool}`. <!-- RT#12 -->
  - `Response{Text, Usage{In, Out}, CostUSD, Model, Provider, Duration}`.
  - Adapters:
    - **Ollama (required local default)**: plain HTTP `/api/chat` NDJSON; `keep_alive` used for unload; resolves to the `gpu` queue. The `ollama` service is a required service in `compose.gpu.yml` (no profile); it has no model until phase 9b, and becomes the seeded tenant default in phase 9c. <!-- RT#7 -->
    - claude-cli: calls the `llm-cli` sidecar; resolves to the `llm` queue; local mode only; disabled when `APP_MODE=saas`.
    - anthropic-api: the official `anthropic-sdk-go`.
    - gemini-api: `google.golang.org/genai`.
- **Selection:** the `llm_settings` table stores the tenant default plus per-action overrides (`outline`, `draft`, `rewrite`, `translate`, `scene_split`, `summary`). Until phase 9c seeds Ollama, the default is claude-cli. Env provides fallbacks. Endpoints `GET/PUT /settings/llm` and `POST /settings/llm/test`. API keys are stored through `envelope`. `registry.QueueFor(ctx, StepRef)` implements the phase 3 `Queue(ctx, StepRef)` for LLM steps (Ollama → `gpu`, others → `llm`) and records `provider_ref`. <!-- RT#6 -->
- **llm-cli sidecar** (a rewrite of the pattern, not a copy) <!-- RT#11 -->:
  - A separate container `llm-cli` built from `deploy/docker/llmcli.Dockerfile` (a tiny Go shim `api/cmd/llmcli` + Node 22 + pinned `@anthropic-ai/claude-code`). The worker image no longer contains the CLI.
  - It holds **only** `/run/secrets/claude_oauth_token` (from `claude setup-token`). No KEK, DB, MinIO or YouTube secrets are mounted. Runs non-root, `read_only: true` rootfs, `tmpfs` for `HOME` and `/tmp`, `cap_drop: [ALL]`, `no-new-privileges`, `mem_limit: 512m`.
  - Network: attached only to `llm_net` (internal) shared with the worker and `egress-proxy`. `HTTPS_PROXY` points to `egress-proxy` (tinyproxy, pinned), whose allowlist holds only the Anthropic hosts the CLI needs (recorded during the step 1 spike).
  - The shim exposes `POST /v1/run` (bearer token from a compose secret shared with the worker only) and streams NDJSON back. Payload on the CLI's stdin, never argv.
  - The argv is a Go `[]string` constant (no shell parsing), exactly:
    `[]string{"-p", "--output-format", "stream-json", "--verbose", "--model", model, "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "", "--disable-slash-commands", "--no-session-persistence", "--system-prompt", fixedSystemPrompt}`
  - Each call gets a fresh `0700` workdir on tmpfs, removed afterwards; env allowlist `PATH`, `HOME`, `LANG`, `HTTPS_PROXY`, `CLAUDE_CODE_OAUTH_TOKEN`.
  - The shim **asserts the `system/init` event reports `tools: []`** and no MCP servers; otherwise it kills the process and returns `cli_tools_enabled` (permanent).
  - Timeouts: ctx (default 10 min), `cmd.WaitDelay=5s`, process-group kill. Weighted semaphore (default 2). NDJSON 4MB line cap, 2MB total output cap, stderr 64KB capped and scrubbed. The `result` event is parsed for `is_error`, `usage`, `total_cost_usd`.
  - Startup self-check (`claude --version` + flag probe against the pinned version); on failure the provider is disabled and the UI shows why.
  - No host credentials file is ever mounted (the old fallback is dropped). If token auth fails, the fallback (validated 2026-09-24) is running the same `llm-cli` service as a host-side Windows process that uses the host's existing Claude Code login, bound to localhost only and requiring a shared-secret header; containers reach it via `host.docker.internal`. The anthropic-api adapter remains available but is not the fallback.
- **Untrusted and derived content** <!-- RT#12 -->: **all story content** (drafts, imports, bible sections, summaries, scene text) is sent as `DataBlock`s, rendered as `<data-{nonce}>…</data-{nonce}>` with a random 16-hex nonce per request and any tag occurrence escaped. `System` holds only fixed server templates. `Tainted` propagates: content derived from any tainted block is stored tainted (phase 6 columns). Structured outputs are validated against a JSON Schema with one retry. No provider ever gets tools.
- **Image:** the `ImageGenerator` interface is implemented by the ComfyUI client (HTTP `/prompt`, WS `/ws?clientId` progress, `/history/{id}`, `/view`, `/upload/image`, `/free`, `/system_stats`). Workflows are server-side templates in `comfyui/workflows/*.json` with a declared parameter map. There is deliberately no gRPC hop for image.
- **gRPC (`proto/loomtale/worker/v1`, all defined here so later phases never edit protos)** <!-- RT#5 RT#15 -->:
  - `worker.proto`: `Health`, `ListEngines` (includes loaded state and VRAM held per engine), `LoadModel`, `UnloadModel`, `GpuStatus`
  - `tts.proto`: `Synthesize(req) returns (stream SynthesizeEvent{progress|result})`
  - `align.proto`: `Align(req) returns (stream AlignEvent)`
  - `train.proto`: `Train(req) returns (stream TrainEvent{progress|log|result})` (ai-toolkit engine in phase 9c)
  - `vision.proto`: `Score(req)`, `Depth(req)` (phase 9c engines)
  - Inputs and outputs are internal presigned URLs from `storage.Internal` with step-length TTL, so the Python worker holds no S3 credentials. A shared bearer token travels in metadata; max message size 4MB. <!-- RT#8 -->
- **Python worker:** `grpc.aio` server, `ModelManager` (one resident engine, asyncio lock, reports CUDA OOM as `RESOURCE_EXHAUSTED gpu_oom`), an engine registry (Protocol `Engine{name, task, license, load, unload, run}`) empty until phase 9b, a pynvml probe (`no_gpu` handled), httpx transfers with size caps. Unknown engine → `FAILED_PRECONDITION engine_not_installed`. Env `HF_HUB_OFFLINE=1`, `TRANSFORMERS_OFFLINE=1`; loaders accept safetensors/GGUF only (`weights_only`, no pickle). <!-- RT#13 -->
- **Residency manager (Go)** <!-- RT#5 -->: implements `ModelResidency` and `GpuProbe`.
  - Budget: at boot with nothing loaded, the probe measures free VRAM (≈10.9GB on this desktop, see phase 1b) and sets `budget_mb = free − render_reserve_mb`. Manifest VRAM estimates are checked against it.
  - `Ensure(m)`: unload every other backend (Ollama `keep_alive:0`, ComfyUI `POST /free {unload_models,free_memory}`, pyworker `UnloadModel`), then **poll `GpuProbe` every 500ms until `free_mb ≥ m.vram_mb + 512`** (timeout 60s → transient error), then load, then **prove residency app-side**: ComfyUI `/system_stats`, Ollama `/api/ps`, pyworker `ListEngines`. `nvidia-smi` per-process data is not used (not reliable on WDDM).
  - `UnloadAll` is used by the `gpu_oom` class in phase 3.
- **Network and supply chain** <!-- RT#13 -->: GPU containers (`comfyui`, `pyworker`, `ollama`) sit on `gpu_net` with `internal: true` (no egress); only the worker joins it, so ComfyUI and pyworker are reachable only from the worker. Model files are fetched by the worker's `models.pull` step (phase 9a) into the `models` volume, mounted read-only in GPU containers. No service mounts `docker.sock`.
- **Egress guard** (`api/internal/netguard`): user-configurable URLs (Ollama and ComfyUI endpoints) must be http(s), match `ALLOWED_PROVIDER_HOSTS`, and are re-checked at dial time with `net.Dialer.Control`. Always blocks 169.254.0.0/16, `::ffff:169.254/112`, 0.0.0.0 and metadata hostnames. Redirects are not followed.

## Architecture
The worker receives a step and resolves its provider through the registry (the provider was fixed at enqueue in `provider_ref`). For a GPU-backed provider it calls `residency.Ensure`, then the provider runs and streams deltas. Deltas go to `StepContext.Progress` and `sse.Publish` (tokens batched every 100ms), and the output is stored as step output (with its taint flag) or an asset. Claude calls go worker → `llm-cli` (HTTP, `llm_net`) → egress-proxy → Anthropic.

## Related files
- Create:
  - `api/internal/providers/{llm,llm/ollama,llm/claudecli,llm/anthropic,llm/gemini,image/comfyui,tts,align,train,vision,registry,residency}/`, `api/internal/netguard/`, `api/cmd/llmcli/`
  - `db/migrations/*_llm_settings.sql`, `db/queries/llm_settings.sql`, `openapi/paths/settings_llm.yaml`, `openapi/schemas/providers.yaml`
  - `proto/loomtale/worker/v1/{worker,tts,align,train,vision}.proto`
  - `workers-python/src/loomtale_worker/{server,model_manager,engines/base,engines/registry,transfer,gpu}.py`
  - `deploy/docker/{pyworker,llmcli}.Dockerfile`, `deploy/egress-proxy/tinyproxy.conf`
- Modify (phase 4 owns both compose files during parallel group C): `deploy/compose.gpu.yml` (pyworker, ollama required service, `gpu_net` internal, worker `WORKER_GPU=true`), `deploy/compose.yml` (llm-cli, egress-proxy, `llm_net`), `openapi/root.yaml`.

## Implementation steps
1. **Spike (1h, gate):** inside the `llm-cli` image with read-only rootfs and tmpfs HOME, confirm `CLAUDE_CODE_OAUTH_TOKEN` auth works with `--setting-sources ""` and `--tools ""` through the egress proxy, record the hosts it needs, confirm `system/init` shows `tools: []`, and record the golden argv in `claudecli/argv_test.go`. If it fails, switch to the validated fallback: run `llm-cli` as a host-side localhost-only process with a shared-secret header.
2. Implement the LLM types, data-block rendering with taint, and the JSON-schema validate+retry helper.
3. Implement the Ollama, anthropic and gemini adapters (streaming; usage mapped to one shape) and `registry.QueueFor`.
4. Implement the llm-cli shim, sidecar image, egress proxy and the Go client adapter, plus the self-check.
5. Add the `llm_settings` schema, registry, endpoints and probe (`/settings/llm/test` runs a 1-token prompt).
6. Implement netguard and use it for every provider HTTP client.
7. Write the protos, run `make gen`, and write the Go TTS, align, train and vision clients (streams mapped to `StepContext.Progress`; `gpu_oom` mapped to the phase 3 class).
8. Implement the Python server, model manager, engine registry, transfer, gpu probe and a non-root image with offline env.
9. Implement the ComfyUI client: submit, WS progress, fetch outputs, stream them to MinIO through `storage.Internal`.
10. Implement the residency manager (budget, VRAM polling, app-side proof, `UnloadAll`) and feed `/gpu` through `GpuProbe`.

## Todo checklist
- [ ] llm-cli sidecar auth spike (gate)
- [ ] LLM interface + data blocks + taint + 4 adapters + QueueFor
- [ ] llm-cli sidecar (isolation, egress proxy, init tools assert)
- [ ] llm_settings + registry + endpoints (AC8)
- [ ] netguard + internal-only GPU network
- [ ] Protos (worker, tts, align, train, vision) + Go clients
- [ ] Python worker skeleton (no models, offline, safetensors/GGUF only)
- [ ] ComfyUI client
- [ ] Residency manager (VRAM polling, app-side proof) + GPU probe

## Performance budget checks
- Adapter overhead ≤5ms beyond the provider. CLI spawn to first delta measured and logged (target ≤3s, sidecar hop included).
- Token SSE events batched every 100ms, so ≤10 events/s per stream.
- A residency switch (unload + VRAM free confirmed + next load start) is timed per backend; target ≤15s, real numbers in phases 9a–9c.
- Python worker idle RSS ≤150MB with no engines.

## Security checklist
- [ ] CLI in its own sidecar: only its token, non-root, read-only rootfs, tmpfs HOME, egress only to Anthropic via proxy, argv as `[]string`, stdin payload, init `tools: []` asserted
- [ ] Prompt-injection corpus (instructions, fake tool calls, closing-tag injection) with a **canary** (e.g. "reply with CANARY-7f3a"): the canary never appears in outputs, and no tool events occur
- [ ] API keys stored encrypted; never returned by the API (only `configured: true`)
- [ ] netguard blocks metadata IP, redirects and non-allowlisted hosts (tests)
- [ ] gRPC bearer token required; pyworker, ComfyUI and Ollama unreachable except from the worker; no host ports; no `docker.sock`
- [ ] `HF_HUB_OFFLINE=1`; pickle loading refused
- [ ] claude-cli refused when `APP_MODE=saas` (ToS)

## Reuse points
- Create: `llm.Provider`, `llm.DataBlock`, `llm.ValidateJSON`, `registry.Resolve(tenant, action)`, `registry.QueueFor`, `netguard.Client()`, `residency.Ensure/UnloadAll`, the ComfyUI `Workflow` runner, and the Python `Engine` protocol. Phases 6–11 consume these and never call a provider directly.
- Reuse phase 2's `envelope`, `audit`, `storage.Internal`, scrubber, and phase 3's `StepContext`.

## Tests
- `scripts/tb.ps1 test`: golden argv, env filtering, data-block escaping and taint propagation, adapters against `httptest` servers in `_test.go`, netguard, ComfyUI protocol replay fixtures, residency polling with a fake probe (never loads before free VRAM is confirmed).
- `scripts/tb.ps1 test-integration`: a **real** claude CLI call through the sidecar (build tag `claudecli`, skipped without a token), the canary injection corpus, and a check that `llm-cli` cannot reach `postgres:5432` or `example.com`.
- `cd workers-python && uv run pytest -q` with a test-only fake engine in `tests/`.
- Manual AC8 check: `PUT /settings/llm` switches claude-cli → gemini (with a key) → back; `POST /settings/llm/test` reports the new provider with no restart.

## Success criteria
- A real claude CLI completion streams to SSE through the sidecar, with no tool events and `tools: []` asserted.
- Switching the provider through settings changes the next call's provider (AC8).
- The Python worker reports `engines: []` and `gpu` status; TTS returns `engine_not_installed` honestly. `/gpu` shows the measured VRAM budget.

## Risks + rollback
- The CLI rejects OAuth-token auth in the sidecar, or a flag changes (Medium×High): pin the CLI version; fallback is the host-side `llm-cli` process (validated 2026-09-24). No host credentials file is mounted under any fallback. <!-- RT#11 -->
- Egress proxy blocks a host the CLI needs after an upgrade (Medium×Low): the self-check fails loudly and the allowlist is updated with the pinned version.
- Subscription ToS grey zone (contract §7, accepted): local-only; SaaS forces an API key.
- Rollback: providers sit behind the registry, so disable an adapter with config. Revert the PR as a unit.

## Next steps
Phase 6 uses the LLM layer. Phase 7 uses image, TTS and align. Phases 9a–9c plug in the real engines.
