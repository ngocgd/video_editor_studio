# Phase 01b: Blackwell + ComfyUI + Qwen-Image-Edit smoke spike (gate) <!-- RT#15 -->

## Context links
- [plan.md](plan.md) · [phase 01 memory budget](phase-01-repo-scaffold-tooling-ci.md) · [phase 09a](phase-09a-comfyui-image-engines.md)
- [local models on RTX 5060 Ti (sm_120, cu128)](../reports/researcher-260924-2128-local-ai-models-rtx5060ti.md) · [image model comparison](../reports/researcher-260924-2145-image-model-comparison-local-vs-paid.md)
- Host facts (red team, 2026-09-24): the desktop already uses ≈5.4GB VRAM, so ≈10.9GB of the 16GB is free. Docker VM = 20GB / 12 vCPU after phase 1 step 0.
- Depends on phase 1 (Docker restarted with the new limits). Runs in parallel with phase 2 (disjoint files).

## Overview
- Priority: P1 (de-risks phases 9a–9c) · Status: done, conditional go (see [results](reports/cook-260925-phase-01b-smoke-spike.md)) · Effort: 2h engineering + download time
- The riskiest assumption in the plan is that ComfyUI on PyTorch cu128 runs on Blackwell `sm_120` inside WSL2 Docker, and that Qwen-Image-Edit fits the measured VRAM (≈10.9GB free) and the 20GB VM RAM. The original plan tested this at hour ~150. This spike tests it at hour ~14 with the smallest possible download, and produces a go/no-go record before phases 3–8 invest in the image path.

## Requirements
- **Downloads are deferred by default.** The executor must ask the user for explicit approval immediately before any model download, stating the exact files, sizes and licences. Without approval, only steps 1–2 (no weights) run and the spike reports `partial`.
- Minimal file set: one Qwen-Image-Edit revision only (2509 or 2511, whichever has a pinned GGUF Q4 build with a verified Apache-2.0 upstream licence), in GGUF Q4, plus its text encoder (fp8) and VAE. No Z-Image, no Qwen-Image, no LoRA. Record the source repo, revision and sha256 of each file. <!-- RT#10 one revision -->
- Files land in the named volume `models` so phase 9a reuses them (the manifest entries are written from this record; nothing is downloaded twice).
- Measure: CUDA device capability, VRAM free before load, VRAM peak, container RSS peak, time per 1024px edit, and whether ComfyUI had to offload to CPU.

## Architecture
Throwaway harness, no app code: `deploy/docker/comfyui.Dockerfile` (draft; pinned ComfyUI commit, PyTorch cu128 wheels, non-root) run with `docker compose -f deploy/compose.gpu.yml run --rm comfyui` on an `internal: true` network, and the ComfyUI-GGUF custom node pinned by commit. Phase 9a hardens the same Dockerfile (serial handoff, so no ownership conflict).

## Related files
- Create: `deploy/docker/comfyui.Dockerfile` (draft), `plans/reports/spike-<date>-blackwell-comfyui.md` (results record).
- Modify: `deploy/compose.gpu.yml` (a `comfyui` service stub with `mem_limit: 10g`, no published ports).

## Implementation steps
1. Build the ComfyUI image. Inside it run `python -c "import torch; print(torch.cuda.get_device_capability(), torch.version.cuda)"`; expect `(12, 0)` and 12.8+. Run a tiny CUDA matmul.
2. Start ComfyUI and query `/system_stats` for the device and VRAM. Record VRAM free with nothing loaded (this is the measured budget input for phase 4 residency).
3. **Ask the user to approve the download list.** On approval, download the minimal set with `huggingface-cli download --revision <sha>` into the `models` volume and record sha256.
4. Submit one Qwen-Image-Edit workflow (1 ref image, 1024px, default steps) through `/prompt`. Record timing, VRAM peak (from `/system_stats` polled every 1s), and container RSS peak (`docker stats`).
5. Write the results record with a go/no-go verdict.

## Go / no-go criteria
- **Go:** capability `(12, 0)` works, the edit completes without OOM, container RSS peak ≤10GB, and time per edit ≤90s.
- **Conditional go:** it completes only with heavy CPU offload or >90s. Phase 9a keeps Qwen-Image-Edit for character sheets only (not per-scene work) and records the cost.
- **No-go:** a CUDA or kernel failure on `sm_120`, or OOM at the Q4 level. Stop and escalate to the user: the contract §6 paid-API fallback for character sheets becomes a user decision before phase 9a starts.

## Todo checklist
- [x] ComfyUI cu128 image builds; `sm_120` smoke passes
- [x] VRAM free baseline recorded
- [x] User approved download list (or spike marked partial) — N/A, weights already downloaded and approved in a prior task; this spike consumed them read-only (see [model download report](../../reports/spike-260924-model-downloads.md))
- [x] One Qwen-Image-Edit run measured (plus a Z-Image Turbo int8 run, per this task's override)
- [x] Go/no-go record written — [conditional go](reports/cook-260925-phase-01b-smoke-spike.md)

## Performance budget checks
- Container RSS ≤10GB (the phase 1 `mem_limit`), VRAM peak ≤ measured free VRAM, time per edit ≤90s.

## Security checklist
- [ ] Download approval recorded; only safetensors/GGUF files; no `trust_remote_code`
- [ ] ComfyUI on an internal-only network, no host ports, non-root
- [ ] Custom node pinned by commit and reviewed before install

## Tests
- The spike itself is the test; its record is the evidence.

## Success criteria
- The results record exists with measured numbers and a go / conditional go / no-go verdict, and the user has seen it.

## Risks + rollback
- A large download without consent (Low×Medium): step 3 is an explicit user gate.
- Rollback: delete the draft service and the `models` volume files; nothing in the app depends on the spike.

## Next steps
Phase 9a starts from this record (verdict, pinned files, measured VRAM budget).
