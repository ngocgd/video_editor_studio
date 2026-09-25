# Code review: phase 04 provider layer + workers (`feat/provider-layer-workers` 5f4fc87 vs `main`)

Scope: 129 files, about 11.4k added lines (about 4k hand-written Go/Py/YAML, the rest generated pb/openapi/client). Checked against the phase-04 spec, plan Validation Log #2, red-team #5/#6/#7/#8/#11/#12/#13, and the cook report.
Evidence:
- Code read.
- `go vet` plus unit tests for `cmd/llmcli`, `internal/providers/...` and `internal/netguard`. All green. Run in a pinned golang container.
- Merged `compose.yml + compose.gpu.yml` config rendered (project `crreview-cfg`, config only).
- Live tinyproxy probe (the `egress-proxy` image was built and run as `crreview-egress-probe`, then removed along with its image).
  - Refused: CONNECT to example.com, 1.1.1.1 and `api.anthropic.com.evil.com` (403 Filtered), and `api.anthropic.com:22` (403 Access violation).
  - Allowed: CONNECT to `api.anthropic.com`, uppercase `API.ANTHROPIC.COM` and `sentry.io`.
  - A plain GET to a non-allowlisted host got 403. **The allowlist cannot be bypassed via CONNECT, IP literals or suffix tricks.**
  - DNS rebinding is not a concern, because the proxy resolves the name itself and the allowlist is host-only.

**Verdict: do not merge yet.** 0 Critical, 9 High, 11 Medium, 8 Low. 6 findings block the merge (H1-H6). All six are small fixes.
Sidecar isolation is solid and verified: non-root, read-only rootfs, tmpfs HOME, only its own token, fixed `[]string` argv, prompt on stdin, env allowlist, constant-time bearer check, process-group kill, and the proxy allowlist.

## High

**H1. The settings API validates providers against the API process's own adapter map, which never contains claude-cli or ollama in the shipped topology. Blocks merge: yes (AC8).**
- Where: `api/cmd/api/providers.go:28-34`, `settingsapi/routes.go:40-53`
- Defect: the rendered compose gives `api` only the `master_key` secret and only the `loomtale_core` network. It has no `llmcli_bearer_token` and `OLLAMA_MODEL` is empty. So `Registry.Providers` is empty.
- Result:
  - `PUT /settings/llm` returns 400 for every provider, including the fallback default `claude-cli`.
  - `GET` reports the default as `available:false`.
  - `/settings/llm/test` always says "provider not configured".
  - AC8 cannot pass in the real stack. No test covers settingsapi at all.
- Fix: validate names against the static provider set (`ollama`, `claude-cli`, `anthropic-api`, `gemini-api`). Source availability and disabled reasons from the worker's status row (see deviation 3). Run `/test` as a short job on the `llm` queue, not inside the API process.

**H2. The claude-cli adapter silently drops `req.System`, so the default provider never sees the per-action task template. Blocks merge: yes.**
- Where: `llm/claudecli/claudecli.go:62-82`, `cmd/llmcli/argv.go:22`
- Defect: the CLI only ever gets the one generic `LLMCLI_SYSTEM_PROMPT`. Outline, draft and scene_split instructions from phase 6 will reach anthropic, gemini and ollama, but not claude-cli, which is the default until 9c. Output will differ per provider, and so will structured-output success.
- Fix: `System` is trusted by contract, so send it on stdin as a leading instruction section outside the data fences. Alternatively, send a template id that the sidecar maps to an allowlisted system prompt. Add a test.

**H3. Any llm-cli parse error except tools-enabled leaves the CLI running and holds a semaphore slot for up to 10 minutes. Blocks merge: yes.**
- Where: `cmd/llmcli/run.go:387-406`
- Defect: on `ErrOutputTooLarge`, a malformed line, or a scanner "token too long" error, the code goes straight to `cmd.Wait()`. Nobody drains stdout, so the process blocks on a full pipe (or keeps generating) until `runCtx` hits the 10-minute timeout. `WaitDelay` only starts counting after exit or cancel.
- Result: with `MAX_CONCURRENT=2`, two such responses stall every Claude call for 10 minutes and keep burning subscription quota.
- Fix: for any `parseErr != nil`, `syscall.Kill(-pid, SIGKILL)` before `Wait`, the same as the tools branch. Add a test with a fake CLI that emits more than 2MB of text and then sleeps.

**H4. Truncated or errored streams are returned as success by all four adapters. Blocks merge: yes.**
- anthropic `anthropic.go:154-199`: ignores the SSE `event: error` (overloaded_error mid-stream), a missing `message_stop`, and `stop_reason:max_tokens`.
- gemini `gemini.go:336-376`: ignores `finishReason` (MAX_TOKENS, SAFETY, RECITATION) and error chunks, and skips undecodable lines with `continue`.
- ollama `ollama.go:269-299`: a `{"error":...}` line decodes as an empty chunk, and a missing `done:true` is not detected.
- claudecli `claudecli.go:118-149`: if no `result` line arrives (sidecar crash or connection cut), it returns `Text:""` as success.
- Result: drafts that are partial, empty or safety-truncated get stored as successful step output with no retry.
- Fix: require a terminal event (`message_stop`, a final chunk with `finishReason`, `done:true`, or a `result` line). Otherwise return a transient error. Surface `max_tokens` and safety stops as typed errors. Add truncation and error-event tests per adapter.

**H5. `GenerateStructured` returns the retry response without validating it. Blocks merge: yes (spec: "validated with one retry").**
- Where: `llm/jsonschema.go:69` (`return gen(ctx, retryReq)`).
- Defect: invalid JSON on the second attempt flows to the caller as valid. There is also no `jsonschema_test.go`.
- Also: `ValidateJSON` (`:36-39`) decodes only the first JSON value, so trailing text is accepted. It should reject anything but whitespace after the value (`dec.More()` or check for EOF).
- Fix: validate the retry result and wrap the failure as `pipeline.ErrValidation` (permanent). Include the prior assistant turn in the retry request.

**H6. The Claude auth-spike gate was never passed, and the host-side fallback from Validation Log #2 is not built. Blocks merge: yes, as a gate.**
- Where: the cook report ("What the user must do", item 5). The report also says no `claude setup-token` was run.
- Defect: no live token call has ever gone through the sidecar and proxy. `allowed-hosts.conf` says it was "recorded during the spike", but no spike with a token ran. `statsig` and `sentry.io` are guesses. `live_test.go` skips in CI.
- Fix:
  - Run `setup-token` once, then run the live corpus test. Record the hosts actually used and the fact that init reports `tools: []` in plan.md.
  - Remove `sentry.io` unless it is observed (it is a generic SaaS exfil sink).
  - Before phase 6, ship the host fallback: the same binary bound to `127.0.0.1`, reusing `authorized()`, which already does the constant-time compare. Refuse to start on any other bind address, and never read `~/.claude/.credentials.json` directly; let the CLI use its own login.

**H7. The residency manager unloads and reloads even when the target model is already resident, and it fails every Ensure when optional ComfyUI is down. Blocks merge: no (latent until OLLAMA_MODEL is set); fix before 9a/9b.**
- Where: `residency/manager.go:119-121,151-161`; `gpu_executor.go:232` calls Ensure on every chunk.
- Result 1: `preferResident` exists to avoid swaps, but Ensure always runs `UnloadAll` and then Load, costing 5-15s per chunk.
- Result 2: comfyui is a manual spike service (`restart: "no"`). If it is not running, `Free` fails, `UnloadAll` returns an error, and every Ollama GPU step fails.
- Fix:
  - Short-circuit when `Current()==target && backend.Resident(target)`. This is the app-side proof, so it stays safe across worker restarts.
  - Treat an unreachable backend as already unloaded: log it, don't fail.
  - Hold `m.mu` or a separate op mutex across Ensure/UnloadAll.

**H8. pyworker cannot reach MinIO, so the presigned-URL transfer design cannot work. Blocks merge: no (no engines yet); a design fix is needed before 9b.**
- Where: `compose.gpu.yml` pyworker `networks: [loomtale_gpu]` (internal), and minio is on `loomtale_core` only. `transfer.py` expects `storage.Internal` URLs (`minio:9000`).
- Fix: either the Go worker streams bytes to and from pyworker (keeping gpu_net sealed), or give minio a network alias on loomtale_gpu and accept that comfyui and ollama can then reach it too. This needs a decision now, because the protos are frozen in this phase (RT#8).

**H9. Adapter errors are not mapped to pipeline sentinels, so permanent failures are retried as transient and Ollama OOM never triggers the gpu_oom policy. Blocks merge: no.**
- Where: `classify.go` defaults to transient; `claudecli.go:139`, `anthropic.go:147`, `gemini.go:329`, `ollama.go:262`.
- Result:
  - `cli_tools_enabled` (spec: permanent), 401/400, `ErrProviderDisabled` and schema failures are each retried 3 times.
  - An Ollama CUDA OOM returns a 500, not `ErrGPUOOM`, so "OOM → full unload + 1 retry" never runs for Ollama.
- Fix: add an `llm.ClassifyHTTP` helper:
  - 400/401/403/404 map to `ErrValidation`; 429/5xx stay transient.
  - `cli_tools_enabled` and disabled map to permanent.
  - "out of memory" or "CUDA" in an Ollama body maps to `ErrGPUOOM`.

## Medium

**M1. netguard does not enforce the allowlist at dial time, its hostname branch is dead code, and one test is a phantom.**
- Where: `netguard.go:93-112`
- `Dialer.Control` receives the resolved IP:port, so the `metadataHostnames` check and the `ip==nil` branch never run in real use. `TestDialControlBlocksMetadataHostname` (`netguard_test.go:49`) passes only because it calls Control with a hostname by hand.
- `ValidateURL` has no callers, and `OLLAMA_URL` is never validated. IPv6 IMDS `fd00:ec2::254` is not blocked.
- The cook report's security-checklist tick for this is false.
- Fix: in `DialContext`, check the host against the allowlist before resolving, check every resolved IP, and call `ValidateURL` at config load.

**M2. Ollama mounts the shared `loomtale_models` volume read-write as root (`compose.gpu.yml:83`).** The spec says models are mounted read-only in GPU containers. Ollama can write into the tree that comfyui loads from, and comfyui can load `.ckpt` pickles. Use a separate `ollama_models` volume or a subpath; `models.pull` owns the writes.

**M3. llm-cli reports healthy with an empty OAuth token, and the flag probe is missing** (`main.go:62-81`, `selfcheck.go`). The spec requires "--version + flag probe" and says the UI should show why the provider is disabled. Set `disabledReason` when the token is empty. Probe flags with `claude -p --help` and check it lists `--tools` and `--strict-mcp-config`.

**M4. No HTTP timeouts on any LLM path.** `http.DefaultClient` is used at `providers.go:34,38,42` and `worker/residency.go:27-28`. `TestLLMSettings` (`routes.go:91`) calls Generate synchronously on the request context, and claude-cli can hold that for up to 10 minutes. Use a transport with dial, TLS and response-header timeouts, plus `context.WithTimeout(30s)` on `/test`. Note that the netguard client's 30s total timeout (`netguard.go:83`) would also cut off long Ollama generations if it were ever used for Stream.

**M5. `Pricing` is never set** (`providers.go:38-42`), so anthropic and gemini always report `CostUSD=0` and any spend cap or cost display is blind. Configure pricing per model, or fail construction when it is unknown.

**M6. Python `ModelManager.run` checks residency and runs the engine outside the lock** (`model_manager.py:94-100`). A concurrent `load(B)` can unload engine A mid-run. The docstring claims run is serialized. Hold the lock for the whole run, or use a reader lock with swap exclusion.

**M7. pyworker is not a residency backend** (`worker/residency.go:34-43`, where `_ = pw`). Ensure and UnloadAll never call `UnloadModel`, which the spec lists, so Python VRAM survives an Ollama switch or an OOM unload. Add a `pyworker` Backend now, since UnloadModel with an empty engine is already implemented.

**M8. Ollama's residency proof compares names exactly** (`ollama.go:346-348`). `/api/ps` reports `llama3:latest` when config says `llama3`, so `proveResident` spins for 60s and then fails. Normalize the implicit `:latest` tag.

**M9. The llm-cli sidecar relays deltas before init is verified** (`ndjson.go:110-127`). `InitOK` is only checked after exit (`run.go:408`). If assistant events arrive before init, text reaches SSE before the tools assertion runs. Reject any assistant or result event while `!InitOK`.

**M10. The BYOK claim does not match the code.** `SettingsAPI.Secrets` is nil (`api/main.go`), so `configured` is never emitted, and there is no path from the envelope-encrypted secrets table to an adapter. See deviation 2.

**M11. PUT /settings/llm is not audited**, unlike the other owner mutations (`pipelineapi/audit.go`). Record it with phase 2's `audit`.

## Low

- L1. `datablock.go:33-38` renders `<data-N> label="x">`: the open tag already ends in `>`, so the label lands inside the fence as stray text. Build the tag as `<data-N label="...">`, or drop the label.
- L2. `server.py:97` sets no `maximum_concurrent_rpcs`. `auth.py:167` accepts `"Bearer "` when the token file is empty; fail at boot instead, the way llmcli does.
- L3. pyworker, ollama and egress-proxy have no healthchecks, pyworker has no read_only, and llm-cli has no `init: true` (reaping orphaned CLI grandchildren) and no `pids_limit`.
- L4. `tinyproxy.conf:62` uses `FilterExtended`, which is deprecated in 1.11.2 (a warning is logged). Switch to `FilterType ere`. Also add a CI step that runs a CONNECT to example.com through the proxy and expects 403. Today CI only tests direct egress.
- L5. In saas mode claude-cli is still put in `Providers` (`providers.go:32-35`), so settings shows it as available and PUT accepts it. Omit it when saas.
- L6. `NewManagerWithBudget` measures the budget without unloading first. If nvidia-smi fails, the budget is 0 and nothing is logged (`manager.go:67-77`, `gpu_probe.go:268`).
- L7. `api/main.go` still carries the stale comment `Probe/Residency: nil, // wired by phase 4`. `live_test.go` has the wrong skip message (it reads the bearer token, not the oauth token). gofmt drift in `cmd/llmcli/main.go` and `cmd/api/providers.go`; CI has no formatter check.
- L8. `tts_service.py:279` yields a fake empty success when an engine is installed but not wired. Abort with UNIMPLEMENTED instead.

## Verdict on the implementer's deviations

1. **Direct REST instead of the SDKs: accept, with conditions.** The surface is small and every caller goes through `llm.Provider`. But the SDKs' value is exactly what is missing here: stream error events, stop reasons, typed errors, and 429/529 backoff. Fix H4, H9 and M4, then record the deviation in plan.md. If those fixes grow past about 150 lines, switch to the SDKs.
2. **BYOK is process-wide until phase 6: accept for `APP_MODE=local` (single operator, the operator's own key) only.** In saas, every tenant would spend the operator's key with no attribution. Refuse to construct anthropic or gemini from files when `APP_MODE=saas`, and drop the "stored through envelope" checklist tick until phase 6 wires tenant-scoped construction.
3. **`/gpu` has no live VRAM: accept that the API must not join gpu_net.** Simplest correct channel:
   - Table `worker_status(worker_id text PK, gpu jsonb, resident_ref text, providers jsonb, updated_at timestamptz)`. The payload holds the GpuSnapshot, backend statuses, llm-cli healthz, and disabled reasons.
   - The worker upserts its row every 5s from a heartbeat goroutine, and immediately after Ensure and UnloadAll.
   - The API reads the freshest row on `GET /gpu` and `GET /settings/llm`. A row older than 15s is shown as `worker_offline`.
   - No NOTIFY is needed. Add `pg_notify` only if the UI later wants SSE push; the existing hub can relay it.
   - The same row fixes H1, because provider availability then comes from the process that actually calls the providers. It adds no new network path.

## Recommended order

H3, H5, H2 (each under 20 lines), then H4, then H1 with the worker_status table. Do the live spike (H6) before tagging phase 4 done. Fix H7, H8, M2 and M7 before 9a/9b.

## Unresolved questions

1. H8: should pyworker get bytes proxied through the Go worker (gpu_net stays sealed), or should minio get an alias on gpu_net? The protos (RT#8) are frozen, so decide before merge.
2. H6: is the owner willing to run `claude setup-token` now, so the gate can be closed with real proxy host evidence?
3. Should `/settings/llm/test` be allowed to spend paid tokens synchronously? It is owner-only but has no rate limit.
