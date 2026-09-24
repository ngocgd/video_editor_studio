# Phase 09a: Model manifest, ComfyUI and image engines (first real download phase) <!-- RT#15 split of the former phase 9 -->

## Context links
- [plan.md](plan.md) · [contract §2 constraints (commercial licences, single GPU slot), §6 model table](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- [local models on RTX 5060 Ti (sm_120, cu128, sequential load)](../reports/researcher-260924-2128-local-ai-models-rtx5060ti.md) · [image model comparison](../reports/researcher-260924-2145-image-model-comparison-local-vs-paid.md)
- [phase 1b spike record](phase-01b-blackwell-comfyui-smoke-spike.md) (go/no-go, pinned Qwen-Image-Edit files, measured VRAM budget) · [phase 1 memory budget](phase-01-repo-scaffold-tooling-ci.md)
- Wireframe: [settings-models](../../docs/wireframe/settings-models.html) (Model manager table)
- Depends on phase 1b (verdict go or conditional go), phase 4 (ComfyUI client, residency, `gpu_net`). End-to-end steps need phase 7's image step; the UI step needs phase 5. Runs in parallel with phase 8.

## Overview
- Priority: P1 · Status: pending · Effort: 26h
- This phase introduces the checksum-pinned model manifest and licence gate, hardens ComfyUI on the real GPU, and makes the image steps real: Z-Image Turbo for scenes, Qwen-Image (GGUF Q4) for thumbnails, and Qwen-Image-Edit (GGUF Q4, one revision) for character sheets. It creates the benchmark harness (image suite) and the Model manager UI, and closes the RAM/VRAM go/no-go gate for the full image stack.

## Requirements
- `models/manifest.yaml` is the single source. Each entry: name, task, engine, licence SPDX, licence URL, source repo, **pinned revision**, the **full transitive file list** (weights, text encoders, VAEs, tokenizers, configs, custom-node assets) with sha256, size, and a VRAM estimate. Only `.safetensors` and `.gguf` weight files are allowed; any pickle (`.bin`, `.pt`, `.ckpt`, `.pth`) is refused by the manifest linter. <!-- RT#13 -->
- `loomtale models pull <name>` (and the `models.pull` step on the `io` queue, priority 4) runs in the **worker** (the only service with egress), downloads resumably into the `models` volume, verifies each sha256, and refuses any entry whose licence is not in `LICENCE_ALLOWLIST` (Apache-2.0, MIT, BSD-2/3). GPU containers mount `models` read-only, run with `HF_HUB_OFFLINE=1` and sit on the internal-only `gpu_net`, so nothing outside the manifest can be fetched at runtime. <!-- RT#13 -->
- The licence check re-runs at engine load. Files already fetched by phase 1b are adopted after sha256 verification, not re-downloaded.
- Image candidates: Z-Image Turbo (scenes), Qwen-Image **GGUF Q4** (thumbnails/text), Qwen-Image-Edit **GGUF Q4, exactly one revision** (2509 or 2511, as chosen in phase 1b) for character sheets and ref edits. Excluded: Qwen-Image-2.1, Flux dev. <!-- RT#4 RT#10 -->
- `comfyui` container: pinned commit, PyTorch cu128, non-root, `mem_limit: 10g`, models read-only, only the custom nodes the workflows need (each pinned by commit and reviewed), reachable only from the worker, no host ports, no `docker.sock`. <!-- RT#13 -->
- Workflows in `comfyui/workflows/` (API format + param map): `scene_txt2img_zimage.json` (+LoRA loader), `thumbnail_qwenimage.json`, `charsheet_qwenedit.json`, `ref_edit_qwenedit.json`.
- **Disk pre-flight** <!-- RT#10 -->: free space on the Docker data disk ≥ manifest size + 40GB headroom, computed after `docker system df` (images ≈53GB already) and VHD growth, not from the ~137GB C: free figure alone.
- **VRAM and RAM budget** <!-- RT#4 RT#5 -->: every manifest VRAM estimate must be ≤ the measured `budget_mb` (≈10.9GB free minus the render reserve). ComfyUI may offload to CPU within its 10g `mem_limit`.
- Benchmark harness (`loomtale bench --suite <name>`), image suite here: 20 scene prompts, 3 character sheets; metrics s/img, VRAM peak (ComfyUI `/system_stats`), container RSS peak (`docker stats`), residency switch time; rows in `model_benchmarks`. Phases 9b and 9c add suites.
- Model manager UI per the wireframe: model, task, licence, size, VRAM, status (not installed / downloading x% / installed / loaded, from app-side residency), install, pause, unload, and the "Commercial-use licences only. One GPU model loaded at a time." note.

## Architecture
`POST /models/{name}/install` creates a `models.pull` step → licence gate → download into `models` → sha256 → installed. ComfyUI discovers files on its read-only mount; `residency.Ensure` loads on demand (one resident at a time) and proves residency via `/system_stats`. `/gpu` `backends` and `resident` are populated through the phase 4 probe (schema owned by phase 3).

## Related files
- Create: `models/manifest.yaml`, `tools/manifestlint/`, `api/internal/models/` (registry, pull step, licence gate), `api/internal/bench/` (harness + image suite), `db/migrations/*_models.sql`, `db/queries/models.sql`, `comfyui/workflows/*.json`, `openapi/paths/models.yaml`, `openapi/schemas/models.yaml`, `web/src/features/models/`, `web/src/routes/_app/settings/models.tsx`.
- Modify: `deploy/docker/comfyui.Dockerfile` (harden the phase 1b draft), `deploy/compose.gpu.yml` (comfyui service final), `openapi/root.yaml`.

## Implementation steps
1. Write the manifest schema, the manifest linter (transitive files, safetensors/GGUF only), the licence gate, the pull step with resume and sha256, and the disk pre-flight.
2. Harden the ComfyUI image (pinned custom nodes including ComfyUI-GGUF, non-root, offline env) and re-run the `sm_120` smoke.
3. Pull Z-Image Turbo, write the scene workflow and run the scene step end to end from the storyboard.
4. Pull Qwen-Image Q4 and adopt the phase 1b Qwen-Image-Edit files; write the thumbnail and character sheet workflows; run each end to end.
5. **Go/no-go gate (full image stack):** 3 consecutive cycles scene → thumbnail → charsheet with the core stack and one render running: no OOM (VRAM or container), ComfyUI RSS ≤10GB, VM swap use ≤2GB. On failure: record numbers, move Qwen-Image to thumbnails-on-demand only, and escalate the contract §6 paid fallback to the user.
6. Write the bench harness and the image suite; record results.
7. Build the Model manager UI.

## Todo checklist
- [ ] Manifest + linter + licence gate + pull/verify + disk pre-flight
- [ ] ComfyUI hardened (cu128, pinned nodes, offline, internal network)
- [ ] Z-Image scene workflow end to end
- [ ] Qwen-Image thumbnail + Qwen-Image-Edit charsheet workflows
- [ ] RAM/VRAM go/no-go gate recorded
- [ ] Bench harness + image suite
- [ ] Model manager UI

## Performance budget checks
- Targets to confirm or revise with measured data: Z-Image Turbo ≤5s per 1080p-class image; residency switch ≤15s; charsheet ≤90s.
- VRAM peak ≤ measured `budget_mb` per model; ComfyUI RSS ≤10GB; no OOM across the step 5 cycles.

## Security checklist
- [ ] Only allowlisted licences load; licence URL and verification date recorded
- [ ] Full transitive file list pinned by revision + sha256; pickles refused; no `trust_remote_code`
- [ ] GPU containers offline (`HF_HUB_OFFLINE=1`, internal network); only `models.pull` in the worker has egress
- [ ] ComfyUI not published to host, reachable only from the worker; custom nodes reviewed and pinned; non-root; no `docker.sock`
- [ ] CUDA base images pinned by digest

## Reuse points
- Reuse phase 4's ComfyUI `Workflow` runner, `residency`, `storage.Internal`; phase 3's GPU executor; phase 5's `VirtualTable`, `StatusChip`, `JobProgress`.
- Create `models.Manifest` (reused by 9b/9c and future remote GPU workers) and `bench` (re-runnable after driver or model upgrades).

## Tests
- `scripts/tb.ps1 test`: manifest parsing, linter refusals (pickle file, missing transitive file), licence gate refusal, checksum mismatch, resume offsets.
- `docker compose -f deploy/compose.yml -f deploy/compose.gpu.yml up -d --wait`, then `loomtale bench --suite image-smoke`.
- A test asserts `comfyui` cannot resolve or reach `huggingface.co`.

## Success criteria
- From the UI: install, load and unload work for each image model; only one GPU model is resident at a time, as shown by `/gpu` from app-side probes (ComfyUI `/system_stats`, Ollama `/api/ps`, pyworker `ListEngines`). <!-- RT#5 -->
- A scene image, a thumbnail and a character sheet are produced for a real episode.
- The go/no-go gate result is recorded with measured VRAM, RSS and swap.

## Risks + rollback
- Blackwell/cu128 incompatibility in a custom node (Medium×High): phase 1b already de-risked the base; pin versions and smoke-test each node.
- RAM pressure from GGUF offload (Medium×High): 10g limit, step 5 gate, and the paid fallback as a user decision.
- Licence changes upstream (Medium×High): pinned revision and URL, re-verified at install.
- Disk fills (Medium×Medium): pre-flight, phase 8 watermark, Library cleanup.
- Rollback: `loomtale models remove <name>`; services live in the gpu override, so the core stack is unaffected.

## Next steps
Phase 9b adds TTS, align and the local LLM.
