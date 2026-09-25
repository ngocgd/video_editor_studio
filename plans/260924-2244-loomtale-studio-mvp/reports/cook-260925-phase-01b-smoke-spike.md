# Phase 1b: Blackwell + ComfyUI + Qwen-Image-Edit smoke spike — results

Date: 2026-09-25 (Asia/Bangkok) · Host: Windows 11 + Docker Desktop WSL2, RTX 5060 Ti 16GB (Blackwell, sm_120), Docker VM 20GB/12 vCPU (verified live: `docker info` → `20972777472` bytes / 12 CPU).

## Verdict: **Conditional go**

Blackwell (`sm_120`) works end to end with ComfyUI + PyTorch cu128. Both Qwen-Image-Edit-2509 (GGUF Q4_K_M) and Z-Image Turbo (int8_convrot) ran a real edit/generation to a valid 1024x1024 PNG with no OOM and no crash. But Qwen-Image-Edit misses the phase's 90s time budget by a wide margin (244s cold, ~184s "warm" because the model gets evicted from VRAM between calls under this card's memory pressure), and its peak VRAM use (~14.06 GiB) leaves only ~1.84 GiB of headroom on this 16GB card even under the unusually light desktop load measured during this spike (~1GB, not the ~5.4GB documented as the host's typical baseline). Per the phase's own criteria this is the conditional-go branch, not go: **phase 9a should scope Qwen-Image-Edit to character sheets only (not per-scene work), run it exclusively (no other GPU consumer active), and budget ~200-250s per edit, not 90s.** Z-Image Turbo int8 is a clean go (25s, comfortable VRAM/RAM margin) and this spike also answers the open question from the model-download report: **yes, the pinned ComfyUI commit natively supports `int8_convrot` quantization** (see Evidence).

## What was pinned

| Component | Pin | Source |
|---|---|---|
| Base image | `nvidia/cuda@sha256:17e2934e1fa96152b14f78078bfbafd0f00f391df995dc6c641a720fce1202bb` (`12.8.1-cudnn-runtime-ubuntu22.04`) | Docker Hub |
| PyTorch | `2.9.1+cu128` | `download.pytorch.org/whl/cu128` (latest stable at time of spike; ships sm_120 kernels since the 2.7 series) |
| torchvision | `0.24.1+cu128` | same index, matched to torch 2.9.x |
| ComfyUI | commit `78368eafee727c52efd121b775e0783c195e5c94` (current `master` HEAD at spike time) | github.com/comfyanonymous/ComfyUI |
| ComfyUI-GGUF | commit `6ea2651e7df66d7585f6ffee804b20e92fb38b8a` (current `main` HEAD at spike time) | github.com/city96/ComfyUI-GGUF — reviewed: pure Python, no build step, single extra dependency `gguf>=0.13.0` |

Model weights: reused as-is from `plans/reports/spike-260924-model-downloads.md` (already downloaded, sha256-verified, in the `loomtale_models` volume). No new downloads in this phase. The volume's subfolder names (`diffusion_models/`, `text_encoders/`, `vae/`) already match ComfyUI's `folder_paths` categories, so the whole volume mounts straight to `/app/ComfyUI/models:ro` with no reshuffling.

## Files created / modified

- Create: `deploy/docker/comfyui.Dockerfile`, `comfyui/workflows/qwen-image-edit-2509-smoke.json`, `comfyui/workflows/z-image-turbo-int8-smoke.json`, `scripts/comfyui-smoke-spike.sh`.
- Modify: `deploy/compose.gpu.yml` (added the `comfyui` service + `loomtale_comfyui` internal network), `.gitignore` (ignore `spike-out/`).

## Evidence

### 1. `sm_120` capability + tiny CUDA matmul

```
$ docker run --rm --gpus all --entrypoint python3 loomtale/comfyui-spike:local -c "..."
torch 2.9.1+cu128 cuda 12.8
device_capability (12, 0)
matmul_ok torch.Size([4096, 4096]) seconds 4.4926
```

`(12, 0)` is `sm_120` — matches. The 4096x4096 matmul on `cuda:0` completed without error.

### 2. VRAM free baseline (nothing loaded)

`GET /system_stats` right after ComfyUI start (via `docker exec`, since the service has no published host port — see Deviations):

```json
"devices": [{"name": "cuda:0 NVIDIA GeForce RTX 5060 Ti : cudaMallocAsync", "vram_total": 17074421760, "vram_free": 15894315008}]
```

≈15.9 GiB usable, ≈14.8 GiB free with nothing loaded. Host `nvidia-smi` at spike start: `984 MiB` used / `16311 MiB` total — i.e. the desktop's GPU load during this spike (~1 GB) was well below the ~5.4 GB documented as its typical baseline elsewhere in the plan. **This matters for the verdict** (see below).

### 3. Qwen-Image-Edit-2509 (Q4_K_M GGUF), 1024px, 20 steps, one ref image

Workflow: `comfyui/workflows/qwen-image-edit-2509-smoke.json` (`UnetLoaderGGUF` → `CLIPLoader(type=qwen_image)` → `VAELoader` → `TextEncodeQwenImageEdit` ×2 (positive w/ ref image, negative empty) → `EmptyLatentImage` → `KSampler` → `VAEDecode` → `SaveImage`).

- Cold run (fresh container, model load from the read-only volume + 20 steps): **244s**, `status_str: success`, output `smoke-qwen-image-edit-2509_00001_.png`, 1024x1024, valid PNG (1.18MB).
- Peak VRAM used: `vram_total (17074421760) - min(vram_free) (1975595096)` ≈ **14.06 GiB**, leaving only **≈1.84 GiB** headroom on this 16GB card, sampled every 1s across the run (`spike-out/05-qwen-vram-poll.csv`).
- Container RSS: `docker stats` start 1.875 GiB → end 4.88 GiB, well inside the phase 1 `mem_limit: 10g`. No CPU offload observed in the ComfyUI logs (dynamic-VRAM staging, no `lowvram`/CPU-fallback messages).
- "Warm" re-run with a different seed (same server process, model theoretically already resident): the diffusion model was evicted from VRAM between calls under this card's memory pressure and had to reload (`Requested to load QwenImage` / `loaded completely` reappeared in the logs) — total **≈184s**, still `success`. A same-input re-submit returned in 0s, but that is ComfyUI's node-level result cache short-circuiting identical inputs, not a real inference measurement, and is called out here so it isn't mistaken for a warm-inference number.
- No CUDA/kernel failure, no OOM, at any point across 3 total Qwen-Image-Edit runs.

### 4. Z-Image Turbo int8 (`z_image_turbo_int8_convrot`), 1024px, 8 steps (turbo defaults)

Workflow: `comfyui/workflows/z-image-turbo-int8-smoke.json`, node graph and widget values (steps=8, cfg=1, sampler=`res_multistep`, scheduler=`simple`, `ModelSamplingAuraFlow` shift=3) copied from the official `comfyui-workflow-templates==0.11.69` template (`image_z_image_turbo_int8.json`, fetched to confirm exact parameters) rather than guessed.

- **25s**, `status_str: success`, output `smoke-z-image-turbo-int8_00001_.png`, 1024x1024, valid PNG (1.10MB).
- Directly answers the open question from `plans/reports/spike-260924-model-downloads.md`: the ComfyUI startup log's `comfy_kitchen` backend capability list explicitly includes `quantize_int8_convrot_weight`, `dequantize_int8_convrot_weight`, and `rotate_int8_convrot_weight` for the `eager` and `cuda` backends, and the run completed cleanly. **This pinned commit natively supports `int8_convrot`.** No bf16 fallback purchase is needed on this basis alone.

### 5. Container RAM / RSS

`docker stats` peak across the whole session: **5.17 GiB**, vs. the phase 1 budget table's `mem_limit: 10g` for `comfyui`. Comfortable margin on the RAM side in all scenarios tested.

## Go / no-go criteria, scored

| Criterion | Qwen-Image-Edit-2509 | Z-Image Turbo int8 |
|---|---|---|
| `sm_120` capability works | yes | yes (same runtime) |
| Completes without OOM | yes | yes |
| Container RSS ≤10GB | yes (≤5.17 GiB) | yes |
| Time per image ≤90s | **no** (244s cold / ≈184s warm-with-reload) | yes (25s) |
| VRAM headroom under typical desktop load | **thin** (~14.06 GiB peak vs. ~14.8 GiB free measured here, and the ~10.9 GiB documented as the plan's typical-load baseline is *less* than the peak usage) | comfortable (int8 unet 6.2GB + TE 5.6GB + VAE 0.3GB ≈12GB total footprint, well under measured free VRAM) |

Per the phase's own rubric ("Conditional go: it completes only with heavy CPU offload or >90s. Phase 9a keeps Qwen-Image-Edit for character sheets only... and records the cost"), Qwen-Image-Edit-2509 lands in **conditional go**, not full go: no CPU offload was needed, but the >90s threshold was missed by 2-2.7x, and the VRAM margin is not safe to assume under the host's normal (not spike-time) desktop GPU load.

## Deviations from the phase spec

1. **`comfyui` has no published port, not even on `127.0.0.1`.** The spec's security checklist says "internal-only network, no host ports"; the earlier compose draft (before this spike ran) had added a `127.0.0.1:8188` publish for convenience, but testing showed Docker silently drops port publishing on a network with `internal: true` (`docker port` returns an empty list; `NetworkSettings.Ports` is `{}` even though `HostConfig.PortBindings` looks correct). Rather than removing `internal: true` to get the port working, this spike removed the port publish entirely and used `docker exec <container> curl ...` for every `/system_stats` and `/prompt` call, which satisfies "no host ports" literally and still lets phase 9a's worker (which will run `docker exec`-equivalent calls from inside the same Docker network, or be added to `loomtale_comfyui`) reach it. `scripts/comfyui-smoke-spike.sh` and the compose comment both document this.
2. **Model volume mounted whole**, not per-category. Because `loomtale_models`'s subfolder names already match ComfyUI's `folder_paths` categories (`diffusion_models`, `text_encoders`, `vae`), the spike mounts the volume once at `/app/ComfyUI/models:ro` instead of one bind per subfolder as the spec's architecture sketch implied. Simpler, same effect; the volume's unused `tts/` subfolder is harmless (not a registered ComfyUI category).
3. **Python 3.10** (not 3.12) inside the image — `nvidia/cuda:12.8.1-cudnn-runtime-ubuntu22.04` (Ubuntu 22.04) ships 3.10 by default; ComfyUI logs warn "Python 3.10 will be EOL on October 31 2026." This is a throwaway spike image; phase 9a's hardened Dockerfile should decide whether to add deadsnakes for 3.12 or accept 3.10 (ComfyUI itself runs fine on 3.10, no functional deviation).
4. **The "1 revision" file set decision from phase 1b's original spec is moot**: weights were already downloaded and approved in a prior task (`spike-260924-model-downloads.md`), so this spike consumed them read-only rather than downloading anything itself, per this task's explicit override.
5. **VRAM measured with the desktop at ~1GB usage, not its documented ~5.4GB typical usage.** This is noted prominently above because it changes the verdict's safety margin; re-run this spike's step 4 (`scripts/comfyui-smoke-spike.sh`) with the desktop under normal load before phase 9a commits to concurrent GPU usage assumptions.

## Cleanup performed

- `loomtale-comfyui-1` stopped and removed (verified: `docker ps -a` shows no `comfyui` container after the run).
- `docker ps -a` confirms only pre-existing, unrelated containers remain (`goclaw-*`, `review-bot-*`).
- `nvidia-smi` after cleanup: `1132 MiB / 16311 MiB` used — back to baseline.
- No writes were made to `loomtale_models` (mounted `:ro`); no `docker system prune` / `volume prune` / image deletion was run.
- `spike-out/` (raw logs, CSVs, history JSON, both output PNGs) is left on disk for inspection but is gitignored; nothing in it is committed.

## Recommendation for phase 9a

- Keep Qwen-Image-Edit-2509 (Q4_K_M GGUF) for character-sheet generation only, run it with no other GPU-heavy service active (single GPU slot, as the plan already assumes), and set its time budget to ~200-250s per edit rather than 90s — either accept that cost for the low-frequency character-sheet use case, or re-open the contract §6 paid-API fallback discussion with the user if 200s+ per character sheet is unacceptable.
- Treat Z-Image Turbo int8 as fully qualified for the scene txt2img path: fast (25s), safe VRAM/RAM margin, no need to fetch the bf16 variant.
- Before finalizing the VRAM budget table for phase 9a, re-measure Qwen-Image-Edit's peak VRAM with the desktop under its normal ~5.4GB load; the ~1.84 GiB headroom measured here at ~1GB desktop load will very likely be negative (i.e., OOM risk) at the documented "typical" 5.4GB desktop load, since peak measured usage (14.06 GiB) already exceeds the plan's documented "10.9GB usually free" figure.
- Reuse `deploy/docker/comfyui.Dockerfile`'s pins (base digest, torch/torchvision versions, ComfyUI + ComfyUI-GGUF commits) as the starting point for the hardened phase 9a image; bump ComfyUI/ComfyUI-GGUF commits deliberately (pin-and-review, not float) rather than re-resolving `HEAD`.

## Unresolved questions

- Should phase 9a spend the ~184-244s/edit cost as-is, or invest in exploring `--lowvram`/`--novram` ComfyUI flags or a smaller quant (Q3/Q2) to trade quality for headroom and speed? Not evaluated in this spike (out of scope: one revision, one quant level, per the original spec).
- Confirm the true desktop-idle vs. desktop-typical VRAM baseline before locking phase 9a's residency budget — this spike ran under lighter-than-documented desktop GPU load (see Deviation 5).
- Phase 9a's worker will need network access to `comfyui`; since `loomtale_comfyui` is `internal: true` and has no host port, confirm the intended access pattern (worker container joined to the same internal network vs. `docker exec` from the host) before hardening the Dockerfile/compose further.

Status: DONE
Summary: sm_120 works; Qwen-Image-Edit-2509 GGUF Q4 completes with no OOM but misses the 90s budget (~184-244s) and has thin VRAM headroom under typical desktop load, so verdict is conditional go (character sheets only, per the spec's own fallback branch); Z-Image Turbo int8_convrot is a clean go and confirms this ComfyUI commit natively supports that quantization.
Concerns/Blockers: VRAM margin for Qwen-Image-Edit should be re-verified under the host's normal (not spike-time) desktop GPU load before phase 9a locks its budget; see Unresolved questions for the worker-to-comfyui network access pattern still to be decided.
