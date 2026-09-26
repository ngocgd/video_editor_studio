# Code review: model manifest, ComfyUI and image engines (round 2)

Branch `feat/comfyui-image-engines` (worktree `.claude/worktrees/lane-b`), reviewed as `git diff main...HEAD` with main at `6991f44` (the merge base). 103 files, +9958/−340. I am an independent verifier. Every result below comes from my own run, not from the cook report or the round 1 report.

**Verdict: ready to merge.** The round 1 High finding (H1) is fixed and I verified the fix live. No Critical or High findings remain. Every automated check passes. Each success criterion that needs model weights is pending because **model downloads are paused by the user**. Nothing was downloaded during this review.

## Scope reviewed
- The full diff again, with a focus on the three commits made after round 1: `e72e1ca` (the host-disk pre-flight), `46613f8` (the compose, worker and CLI wiring) and `fadd437` (the cook report).
- `api/internal/models` (download, store, steps), `api/internal/modelsapi`, `api/internal/providers/image/comfyui` (engine, workflow, backend), `api/internal/providers/residency`, `api/internal/bench`, and the `api/cmd/{api,worker,loomtale}` wiring.
- The migration and queries, the OpenAPI `x-min-role` values, `deploy/compose.gpu.yml`, `deploy/docker/comfyui.Dockerfile`, and `web/src/features/models`.

## Round 1 findings: status

| Item | Status | Evidence |
|---|---|---|
| **H1**: the disk pre-flight read the Docker VM's virtual disk | **Fixed** | `preflight` now requires `min(host free, volume free) >= remaining + 40 GB` and refuses with `ErrHostDiskUnknown` when `HostDiskDir` is unset or cannot be read. `compose.gpu.yml` bind-mounts `${MODELS_HOST_DISK_SOURCE:-./host-disk}` read-only at `/host-disk` in `worker` and `cli`, and sets `MODELS_HOST_DISK_DIR`. I confirmed both mounts in the output of `docker compose ... --profile cli config`. **Live probe:** a read-only bind mount of a host directory reports `C:\ 975721468 1K-blocks, 100103744 available`, which matches the host's `(Get-PSDrive C).Free = 102506237952`. The container overlay reports 818,900,568 KB available, the virtual disk's maximum. The mechanism therefore measures the real drive. The unit tests `TestInstallDiskPreflightIsBoundedByTheHostDisk` and `TestInstallDiskPreflightRefusesWithoutAHostDiskFigure` pass and show that no request is sent before a refusal. |
| M1: a pull that hits its deadline stays `downloading` | Open (Medium) | `PullStep.Run` still treats any `ctx.Err()` as a pause. I checked the recovery path: the row stays `downloading`, so the UI offers Pause to the owner. `PauseModelInstall` flips the row to `paused` whatever the step state, and `CancelStep` on a terminal step returns `ErrNotFound`, which the handler tolerates. Resume then works. This is recoverable by the user, so it is not blocking. |
| M2: concurrent pulls that share files race on `.staging/<sha>.part` | Open (Medium) | There is still no per-file lock. Resuming a download while the paused step has not yet stopped hits the same race. `LoadGate`'s size check catches a short file, so there is no silent corruption at load time. |
| M3: model actions are deployment-wide but authorised per tenant | Open (Medium, product decision) | Not changed. This is fine for a single-studio MVP. |
| L1–L4 | Open (Low) | Not changed. |
| L5: whether the hardened image was built | Unchanged | The current Dockerfile is still unbuilt, because building it downloads PyTorch cu128 wheels, which falls under the pause. |

## New findings (round 2)

### Low
- **N1.** The pre-flight does not reserve space for other pulls running at the same time. Two installs started together each see the same free space. The 40 GB headroom covers the current manifest (the largest single entry is 22.9 GB), but the check is not strictly conservative. It can be fixed together with M2 (a per-file lock and a shared reservation).
- **N2.** If Docker's data disk is on a different drive from the checkout and `MODELS_HOST_DISK_SOURCE` is not set, the pre-flight measures the wrong drive. This is documented in `.env.example` and in `compose.gpu.yml`, so it is acceptable as an operator setting.
- **N3.** The branch adds six "phase 1b" references in comments and in workflow `description` strings (`download.go`, `workflow.go`, `manifest.yaml`, `comfyui.Dockerfile`, `charsheet_qwenedit.params.json`). The execution rules forbid plan and phase IDs in code. Main already has 82 such references, so this follows the existing practice and does not block the merge. It is worth a cleanup pass later.

### Checked and fine (re-verified this round)
- **Downloader:** https-only host allowlist, also enforced on redirects. Range resume, with a sha256 over the whole file. The body is cut off at the pinned size + 1. Rename is atomic. The pre-flight runs before any network request.
- **RBAC:** `x-min-role` is viewer for list, owner for install and pause, and editor for load and unload. The UI gates the same actions (`rowAction`). The integration test proves an editor gets 403 on install.
- **Workflow templates:** unknown parameters are refused, upload names are generated on the server, and ComfyUI's own value lists validate LoRA names.
- **Residency:** the wait is capped at the budget, and the worker logs a warning at boot for any estimate above the budget.
- **Secrets:** none are committed. `deploy/host-disk/` is gitignored. I never read `secrets/claude_oauth_token.txt`.

## Verification (run by me)

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` | exit 0. golangci-lint `0 issues`, tenantctx and `lint-tenant-queries: OK`, `manifest lint: OK (4 models, 4 workflows)`, ruff `All checks passed!`, `go test ./... -race` all ok (bench, models, modelsapi, comfyui and residency included), pytest `26 passed` |
| Generated-code drift | `git status --short` empty and `git diff --exit-code` clean after `gen` |
| web `npm run typecheck`, `npm run lint` | pass |
| web `npm test` | 8 files, 38 tests passed |
| `npx vite build` + `npm run budget-check` | pass. Authenticated shell 159.73 KB of 200 KB. `models` route chunk 2.39 KB gzip of 80 KB. CSS 6.23 KB |
| Integration (`loomtale-b`, compose + integration overlay, heavy lock, `--build`) | **56 top-level tests passed, 0 failed** (29.2 s), including all 4 model manager tests. Torn down with `down -v` |
| Playwright e2e (`compose.yml` only, owner seeded, `--workers=1`) | **2 passed**: `models.spec.ts` (listing, blocked licence, install, pause) and `smoke.spec.ts`. The first bring-up failed with `service "minio-init" didn't complete successfully: exit 1` straight after the integration stack's `down -v`. A clean retry under the lock, with `--build`, came up healthy and passed. `compose.yml` and minio are untouched by this branch. Torn down with `down -v` |
| Host-disk statfs, live | Bind mount 100,103,744 KB free, host `C:` 102.5 GB free, container overlay 818.9 GB. The fix measures the right disk |
| Live GPU and ComfyUI stack | **Not re-run.** `comfyui`, `pyworker` and `ollama` are unchanged since round 1 (`git diff --stat c6917a4 HEAD` touches only the downloader, the tests, the host-disk wiring and the report). Bringing up the GPU overlay could pull the Ollama image or rebuild ComfyUI (PyTorch wheels), which falls under the pause. Round 1's independent evidence still stands: isolation 8 of 8 ok, RTX 5060 Ti on pytorch 2.9.1+cu128, 636 MiB RSS of 10 GiB |

## Success criteria

| Criterion | Status |
|---|---|
| Install, load and unload from the UI for each image model, with one resident model shown by `/gpu` | **Partial.** Install and pause work from the UI (e2e) and through the API (integration). Load and unload queue their gpu steps, and a load of an uninstalled model is refused. A real install, load and unload is **pending: model downloads paused by the user** |
| A scene image, a thumbnail and a character sheet produced for a real episode | **Pending: model downloads paused by the user.** It also needs the storyboard and scene image step from lane A, which is not yet on main |
| Go/no-go gate recorded with measured VRAM, RSS and swap | **Pending: model downloads paused by the user.** The `image-gate` suite and `scripts/bench-image.sh` exist and are unit-tested |
| Disk pre-flight (requirement) | **Met.** Unit tests plus the live statfs probe above |
| Every manifest VRAM estimate ≤ the measured `budget_mb` | Met by the round 1 measurement (budget 14134 MB, largest estimate 11500 MB). The boot warning guards regressions |
| Performance targets and the `sm_120` smoke on the hardened image | **Pending: model downloads paused by the user** (weights and the PyTorch wheel download) |

## Blocking items
None. M1 and M2 are recommended before the first real pull, but neither blocks the merge.

## Unresolved questions
- Is the `minio-init` exit 1 on a bring-up straight after another project's `down -v` a known flake? Lane A's stacks may hit it too. Its logs were lost when the stack was torn down, and the retry passed.
- Should M1 and M2 be fixed before downloads resume, or be tracked for phase 9b, which adds more pulls that share files?
- M3 (restrict model management to a platform-level role before a multi-tenant or SaaS mode) is still a product decision.
