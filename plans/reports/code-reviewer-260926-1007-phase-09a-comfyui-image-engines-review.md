# Code review: model manifest, ComfyUI and image engines (round 1)

Branch `feat/comfyui-image-engines` (worktree `.claude/worktrees/lane-b`), reviewed as `git diff main...HEAD` with main at `6991f44` (the merge base, so nothing is missing from main). 101 files, +9749/−340. I am the independent verifier: every result below is from my own run, not the cook report.

**Verdict: not ready to merge.** One High finding is still open (the disk pre-flight measures the wrong disk). Every automated check passes. The success criteria that need model weights are pending because model downloads are paused by the user.

## Scope reviewed
- `api/internal/models` (manifest, linter, licence gate, downloader, store, steps), `api/internal/modelsapi`, `api/internal/bench`, the `api/cmd/{api,worker,loomtale}` wiring, the ComfyUI engine, backend and workflow templates, the residency cap, and the worker status probes.
- `db/migrations/20260926100000_models.sql`, `db/queries/models.sql`, `openapi/{paths,schemas}/models.yaml`.
- `deploy/compose.gpu.yml`, `deploy/docker/comfyui.Dockerfile`, `deploy/comfyui/constraints.txt`, and the scripts `bench-image.sh` and `test-comfyui-isolation.sh`.
- `web/src/features/models`, the route, the navigation entries, and `web/e2e/models.spec.ts`.

## Findings

### High

**H1. The disk pre-flight reads the Docker VM's virtual disk, not the host disk the phase requires it to protect.** Files: `api/internal/models/download.go` (`preflight`) and `diskfree_unix.go`.
- `diskFree` calls `statfs` on the models volume inside the worker container. On Docker Desktop/WSL2 that filesystem is the dynamically growing `ext4.vhdx`, so it reports the VHD's maximum size, not real free space.
- Measured on this host just now:
  - `docker run busybox df -m` on a fresh volume shows **803,196 MB available**.
  - Host `C:` has **96 GB free**.
  - `docker system df` reports images 123.1 GB, volumes 54.7 GB and build cache 77.4 GB.
- The phase requirement says the check must run "on the Docker data disk … computed after `docker system df` … and VHD growth, not from the C: free figure alone". As built, the check passes whatever the host has left. A 22.9 GB pull could therefore fill `C:`, which stalls the VM and risks the Postgres volume (data loss). The unit test only covers an injected `FreeBytes`, so it cannot catch this.
- The cook report marks the pre-flight as done.
- Fix options:
  - (a) Let the worker read a host-side free-space figure: a small host script (like the bench RSS sampler) writes it, or it is stored in a settings or worker-status row. The pre-flight then uses `min(statfs, host free − expected VHD growth)` and refuses when that figure is missing or stale.
  - (b) As a minimum, add a configured models-disk budget (`MODELS_DISK_BUDGET_BYTES`, required in the GPU overlay) and check the need against it, plus the current size of the models volume.
- Either fix needs a test that proves the host figure bounds the check.

### Medium

**M1. A pull that times out or runs out of retries leaves `model_installs` stuck at `downloading`.** File: `api/internal/models/steps.go` (`PullStep.Run`).
- io-queue jobs time out after 2 h (`pipeline.QueueTimeouts`) and `models.pull` has no per-kind override. The qwen-image-edit entry is 22.9 GB, so at under ~3 MB/s one attempt cannot finish.
- On a deadline `Run` takes the `ctx.Err() != nil` branch, which is meant for pause, and returns before `MarkModelInstallFailed`. After `MaxTransientAttempts` (3) the step fails, but the row stays `downloading`. The UI then shows a frozen progress bar and `POST /install` answers 409 "download already running" until an owner happens to press Pause.
- Fix: treat only an explicit pause as a pause (check that the row is `paused`, or `errors.Is(ctx.Err(), context.Canceled)`). On the last attempt, mark the row failed. Consider adding a `models.pull` entry in `KindTimeoutOverrides` below the 4 h rescue.

**M2. Concurrent pulls that share files race on the same staging file.** File: `api/internal/models/download.go`.
- qwen-image and qwen-image-edit-2511 share the text encoder and VAE (9.6 GB). The io queue runs 5 workers, and `partPath` is keyed only by sha256, with no lock.
- Two installs therefore write the same `.staging/<sha>.part` through independent handles. Possible outcomes:
  - The second writer's `os.Rename` fails with ENOENT, a spurious transient failure.
  - A 200 response makes `restart()` truncate an inode that the other writer has already renamed into place and marked verified in `model_files`. The DB then says verified while the file on disk is short, until the writer finishes. `LoadGate` does catch the size mismatch.
- Fix: take a per-file lock for the whole `downloadFile` (for example `flock` on the `.part`, or a Postgres advisory lock keyed by the sha) and re-run `inspect` after acquiring it.

**M3. Model actions are deployment-wide but authorised per tenant.** File: `api/internal/modelsapi/handler.go`.
- An owner of any tenant can pause another tenant's download, because `CancelStep` runs in `started_by_tenant_id`.
- Editors of any tenant can load or unload the single shared GPU model at interactive priority.
- This is fine for a one-studio deployment. Before a SaaS mode it should be a recorded product decision, or be restricted to a platform-level role. It is not blocking for the MVP.

### Low
- **L1.** `PauseModelInstall` turns every database error into 409 "no download is running". Only `pgx.ErrNoRows` should map to 409.
- **L2.** A Ctrl-C during `loomtale models pull` marks the row `failed`, not `paused`. The CLI also downloads from the `cli` container rather than the worker (the spec says the worker is the only downloader). The profile is opt-in, so treat this as a documented deviation.
- **L3.** ComfyUI inputs from `UploadImage` and outputs accumulate on `comfyui_scratch` and are never cleaned up.
- **L4.** `Store.Remove` protects only files of `installed` models, not of a model that is `downloading` at the same time.
- **L5.** The cook report says the hardened image "has not been built yet". In fact `loomtale/comfyui:local` exists (created 2026-09-26T02:40Z, with the hardened CMD flags but the old entrypoint without the scratch `mkdir`). What is true is that the **current** Dockerfile is unbuilt: an offline rebuild (`docker build --network=none`) misses the cache at the runtime-stage `apt-get` layer, so rebuilding needs network downloads (PyTorch wheels and apt packages), which are paused.

### Checked and fine
- **Licence gate:** it runs at install (API, worker, CLI) and at load (`LoadGate` inside `comfyui.Backend.Load`). Refusal is permanent (`licence_refused`). The blocked RAIL++ entry is shown as blocked in the API, CLI and UI.
- **Linter:** refuses pickles, unclean or escaping paths, unpinned revisions, bad sha256 values, conflicting pins, unknown YAML keys, and workflow loader files missing from the entry. It is wired into `make lint`.
- **Downloader:** https-only host allowlist that also applies to every redirect, resumable Range requests, a sha256 over the whole file including the resumed prefix, a body limit, atomic rename within the volume, and adoption of existing files after hashing. The pinned manifest URLs are the only input, so there is no SSRF surface.
- **RBAC:** `x-min-role` is set on all five operations (list viewer, install/pause owner, load/unload editor) and enforced by the spec-built `rbac.Middleware`. Integration tests prove 403 for an editor on install.
- **SQL:** the new tables are deliberately global (one GPU per deployment). The tenant-query lint passes. Default privileges give `loomtale_app` access and no DDL.
- **Residency:** the free-VRAM wait is capped at the budget, so an over-budget estimate cannot dead-wait, and a unit test covers it. `/gpu` backends now carry `loaded` from app-side probes (ComfyUI `/system_stats`, Ollama `/api/ps`, pyworker `ListEngines`).
- **ComfyUI hardening in compose:** internal `loomtale_gpu` only, no ports, read-only root filesystem, models mounted `:ro`, `pids_limit`, `mem_limit: 10g`, `cap_drop: ALL`, `no-new-privileges`. The CUDA base, ComfyUI and ComfyUI-GGUF are pinned by digest or commit, and Python packages by the constraints file.
- **Workflow templates:** only parameters named in the parameter map are accepted. Upload names are generated server-side. HTTP 400 from ComfyUI is classified as validation, and OOM as `gpu_oom`.

## Verification (run by me)

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` | exit 0. golangci-lint 0 issues, tenant-query lint OK, `manifest lint: OK (4 models, 4 workflows)`, ruff clean, Go tests with `-race` all ok (models, bench, modelsapi, comfyui, residency among them), pytest 26 passed |
| Generated-code drift (host `git diff --exit-code` on the db/httpapi/workerpb/pb/web gen paths, openapi bundle, migrations copy, `api/internal/models/assets`, routeTree) | clean, `git status` clean after `gen` |
| web typecheck, lint, vitest | pass. 38 tests in 8 files |
| `npx vite build` + `npm run budget-check` | pass. Shell 159.73 KB of 200 KB, models chunk 2.39 KB of 80 KB |
| Integration (`loomtale-b`, compose + integration overlay, heavy lock) | **56 passed, 0 failed.** All 4 model tests pass. Stack torn down with `down -v` |
| Playwright e2e (`--workers=1`) | **2 passed** (smoke and the model manager: listing, blocked licence, install, pause). Torn down |
| ComfyUI isolation (GPU overlay, existing `loomtale/comfyui:local` image with the fixed entrypoint via a throwaway overlay, no pull) | **8 of 8 ok.** On the GPU: `Device: cuda:0 NVIDIA GeForce RTX 5060 Ti`, pytorch 2.9.1+cu128, total VRAM 16283 MB, RSS 636 MiB of 10 GiB |
| Disk pre-flight on the real host | **fails its purpose** (H1): container statfs 803 GB free, host `C:` 96 GB free |

**ComfyUI healthcheck.** My first ComfyUI run crash-looped with `Read-only file system: '/app/ComfyUI/user'`. The cause was my throwaway overlay, not the repository: when compose overrides `entrypoint`, it drops the image's CMD, and with it the `--user-directory /scratch/user` flags. I re-ran with those flags restated in the overlay, which reproduces the committed Dockerfile's ENTRYPOINT and CMD exactly. That run was `up --wait` → **healthy, 0 restarts**; isolation 8 of 8 ok; `/system_stats` reported pytorch 2.9.1+cu128, ComfyUI 0.37.0, RTX 5060 Ti with 16283 MB total and 15158 MB free; the log showed "Starting server"; RSS was 636 MiB of 10 GiB. Every stack was torn down with `down -v`. Nothing was pulled or downloaded.

## Success criteria

| Criterion | Status |
|---|---|
| Install, load and unload from the UI for each image model, with one resident model shown by `/gpu` | Install and pause are observable (e2e and integration). The load and unload steps are queued on the gpu queue at the right priority, and a load of an uninstalled model is refused. A real install, load and unload is **pending: model downloads paused by the user** |
| Scene, thumbnail and character sheet produced for a real episode | **Pending: model downloads paused by the user.** It also needs the image step from the storyboard/scene phase, which is not merged yet |
| Go/no-go gate recorded with measured VRAM, RSS and swap | **Pending: model downloads paused by the user.** The `image-gate` suite and the RSS sampler exist and are unit-tested |
| sm_120 smoke on the hardened image | The GPU starts under the hardened compose service. A rebuild of the current Dockerfile is **pending: model downloads paused by the user** (large wheel downloads) |
| Disk pre-flight (requirement) | **Not met** (H1) |

## Blocking items
1. H1: the disk pre-flight must be bounded by real host (Docker data disk) free space, with a test.

Recommended in the same round: M1 and M2, since both leave the install state wrong in normal use.

## Unresolved questions
- Should model management be restricted to a platform-level role before a multi-tenant or SaaS mode (M3)?
- Which host free-space source is preferred for H1: a host sampler script, or a configured disk budget?
