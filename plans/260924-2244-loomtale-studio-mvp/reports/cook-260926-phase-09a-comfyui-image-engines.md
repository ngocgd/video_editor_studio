# Phase 9a — Model manifest, ComfyUI and image engines

Branch: `feat/comfyui-image-engines` · Worktree: `.claude/worktrees/lane-b` · Base: `main` @ `6991f44` (merged in)
Status: DONE_WITH_CONCERNS. Everything that does not need model weights is built and verified. Every criterion that needs a downloaded model is pending because **model downloads are paused by the user**.

## What shipped

- **Model manifest** (`models/manifest.yaml`, embedded into the Go binaries by `make gen`): z-image-turbo (scenes), qwen-image GGUF Q4 (thumbnails), qwen-image-edit-2511 GGUF Q4 (character sheets and ref edits, one revision), and illustrious-xl-v1.1, which is listed as blocked because its licence is not allowlisted. Each entry pins the repo revision, the full transitive file list with sha256 and size, the licence SPDX, URL and verification date, and a VRAM estimate.
- **Linter** (`loomtale models lint`, run by `make lint`): it refuses pickle formats, unclean paths, unpinned revisions, bad checksums, and workflow loader files that are missing from the entry. It lives in `api/internal/models/lint.go`, not in a separate `tools/manifestlint/`.
- **Licence gate**: only Apache-2.0, MIT, BSD-2-Clause and BSD-3-Clause pass. It runs at install and again at load (`LoadGate`).
- **Downloader**: resumable Range downloads into `.staging/<sha>.part`. The sha256 covers the whole file. A host allowlist (huggingface.co and hf.co, https only) also applies to redirects. Oversized bodies are cut off. Before downloading, a disk pre-flight checks that free space is at least what remains to download plus 40 GB of headroom. Files already on disk are adopted after hashing instead of being downloaded again.
- **Steps**: `models.pull` runs on the io queue at priority 4, and pausing it keeps the partial file. `models.load` and `models.unload` run on the gpu queue. The steps are registered in the worker only when a GPU and a models volume are both present. The API process only enqueues them.
- **ComfyUI engine**: parameterised workflow templates with parameter maps (`comfyui/workflows/*.json`: scene_txt2img_zimage with a LoRA loader, thumbnail_qwenimage, charsheet_qwenedit, ref_edit_qwenedit). Models load through warm-ups. Residency is proven through `/system_stats`, and the backend residency probes feed `/gpu`.
- **Hardened ComfyUI image** (`deploy/docker/comfyui.Dockerfile`): two stages, the CUDA base pinned by digest, ComfyUI and ComfyUI-GGUF pinned by commit, and every Python package pinned by `deploy/comfyui/constraints.txt`. It runs as a non-root user with offline HF env, `--disable-api-nodes` and `--reserve-vram 1`. `compose.gpu.yml` makes the root filesystem read-only, puts writes on a scratch volume, sets `pids_limit`, adds a healthcheck and a `models-init` chown one-shot, mounts the models volume writable only in the worker and the `cli` profile service, and publishes no host port.
- **Bench harness** (`loomtale bench --suite image|image-smoke|image-gate`): records s/img, VRAM peak, torch peak, ComfyUI RSS (host-sampled by `scripts/bench-image.sh`), VM swap and residency switch time. Rows go into `model_benchmarks`. The go/no-go gate is evaluated from the same results.
- **Model manager API and UI**: `/models` list, install, pause, load and unload, with owner-only install and pause. `web/src/routes/_app/settings/models.tsx` shows model, task, licence, size, VRAM and status (not installed, downloading x%, paused, installed, loaded, blocked), plus the "Commercial-use licences only. One GPU model loaded at a time." note.
- **Isolation test** (`scripts/test-comfyui-isolation.sh`): checks DNS and TCP egress, published ports, non-root user, read-only root filesystem, read-only models mount, docker.sock and the offline env.

### Fixes made in this session (on top of the earlier agent's 7 commits)
- I committed the earlier agent's uncommitted work (Dockerfile hardening, compose wiring, constraints, bench and isolation scripts, and a lint fix to a test) after reviewing it and checking it with `docker compose config`.
- The CLI `models list` did not check the errors returned by tabwriter writes (errcheck lint failure).
- **Residency could never load an over-budget model.** Ensure waited for the manifest's full VRAM estimate to be free, so a model estimated above the budget (it would offload the excess to CPU RAM) could only time out. The wait is now capped at the measured budget, the worker logs every estimate that exceeds the budget at boot, and a unit test covers it.
- **The ComfyUI container crashed on a fresh scratch volume**: `main.py: error: argument --user-directory: The path '/scratch/user' does not exist`. The entrypoint now creates the four scratch directories. I found this during the live run described below.
- **Integration suite: 3 tenant-isolation tests failed with 429.** The model manager tests' 5 logins used up the shared per-IP login bucket (20 per hour). Those tests now reset the per-IP login bucket when they finish.

## Verification

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` | pass. golangci-lint 0 issues, ruff clean, manifest lint clean, all Go packages ok with `-race`, pytest 26 passed |
| Generated-code drift (host `git status` after `gen`) | clean, no diff |
| New Go tests | models 22, bench 5, modelsapi 3, comfyui engine and workflow 10, residency +1 (the over-budget load) |
| `web`: typecheck, lint, vitest | pass, 38 tests in 8 files |
| `npx vite build` + `npm run budget-check` | pass. Authenticated shell 159.73 KB of 200 KB. Models route chunk 2.39 KB gzip of 80 KB |
| Integration (`loomtale-b`, compose + integration overlay, heavy lock) | **56 passed, 0 failed, 59 s** (first run: 3 failures from the 429 problem above, since fixed) |
| Playwright e2e (`--workers=1`) | **2 passed** (smoke, and the model manager page: listing, blocked licence, install, pause) |
| ComfyUI isolation, live (GPU overlay, see deviation 1) | **8 of 8 ok**: no DNS for huggingface.co, no TCP to 1.1.1.1, no host port, non-root, read-only root filesystem, read-only models mount, no docker.sock, HF offline env |
| ComfyUI on the RTX 5060 Ti | started: `Device: cuda:0 NVIDIA GeForce RTX 5060 Ti`, pytorch 2.9.1+cu128, total VRAM 16283 MB, RSS 720 MiB of the 10 GiB limit when idle |
| `loomtale models list` (cli profile, live DB) | pass. 4 entries; illustrious-xl-v1.1 is `blocked` |
| `loomtale bench --suite image-smoke`, no weights | exit 1 as expected. Measured **VRAM budget 14134 MB** (free VRAM minus the 1024 MB render reserve). Each case failed with the honest `engine_not_installed: … is not verified (install the model first)`. VM swap peak 93 MB |

## Success criteria

| Criterion | Status |
|---|---|
| Install, load and unload from the UI for each image model, with one resident model shown by `/gpu` | **Partial.** Install and pause work from the UI (e2e), load and unload queue their steps (integration), and a load without weights is refused as not installed. A real install, load and unload is **pending: model downloads paused by the user** |
| Scene image, thumbnail and character sheet produced for a real episode | **Pending: model downloads paused by the user.** It also needs phase 7's image step, which is not yet merged |
| Go/no-go gate recorded with measured VRAM, RSS and swap | **Pending: model downloads paused by the user.** The harness (`image-gate` suite) and the RSS sampler script are ready |
| Every manifest VRAM estimate ≤ measured `budget_mb` | Measured budget today is 14134 MB. The largest estimate is 11500 MB, so all fit. The worker logs a warning at boot if one ever does not |
| Performance targets (Z-Image ≤5 s, switch ≤15 s, charsheet ≤90 s) | **Pending: model downloads paused by the user** |
| Re-run the `sm_120` smoke on the hardened image | **Pending: model downloads paused by the user.** Building the hardened image downloads PyTorch cu128 wheels (several GB), which falls under the pause. The same pins already ran on the spike image today |

## Deviations
1. **The live ComfyUI checks used the phase 1b spike image** (`loomtale/comfyui-spike:local`), run through the hardened compose service definition via a throwaway overlay that was not committed. Building the hardened image was skipped because it would download PyTorch and other large wheels while downloads are paused. The service-level hardening (network, read-only root filesystem, mounts, user) is therefore verified. The image-level changes (two-stage image, constraints file) have not been built yet.
2. The linter is a `loomtale models lint` subcommand in `api/internal/models`, not a separate `tools/manifestlint/` module. It is wired into `make lint`.
3. Adopting the phase 1b files is implemented (hash, then mark verified), but it has not been exercised live. The spike's files sit in another project's `loomtale_models` volume, which I did not touch.

## Follow-ups (when downloads resume)
- Build `loomtale/comfyui:local`, re-run `scripts/test-comfyui-isolation.sh`, and pull the three allowlisted models through the UI.
- Run `scripts/bench-image.sh image-smoke`, then `image` and `image-gate` (3 cycles with a render running). Record VRAM, RSS and swap in this report, and escalate the paid fallback if the gate fails.
- Wire the scene, thumbnail and character sheet steps to the engine once phase 7 is merged, and produce them for a real episode.

## Unresolved questions
- Should the hardened ComfyUI image build (PyTorch wheels, about 3–4 GB) count as a "model download" under the pause, or may it run before model downloads resume?
- The per-IP login bucket of 20 per hour is a shared limit for the whole integration suite. Lane A's new tests may hit it too. Should a shared helper reset it, instead of each test file doing so?
