# Cook report: phase 8, render pipeline and library

Branch `feat/render-pipeline-library` (worktree `.claude/worktrees/lane-a-8`), built on the phase 7 branch `feat/characters-storyboard` (first at `fffb9f1`, then merged again at `71ac60e`, which only added phase 7 review documents). Not merged or pushed.

Status: DONE_WITH_CONCERNS. Every requirement of the phase file is implemented, and every host check passes. The Docker verification is pending because Docker's data disk is down: the toolbox `gen lint test`, the integration suite, the Playwright spec, the zoompan benchmark gate and the GPU NVENC check have not run. The user asked for code only in this run and will restart Docker for the verification.

## What shipped

- **Schema** (`db/migrations/20260927100000_renders.sql`, inside lane A's range):
  - `render_settings` per episode and language. `render_manifests` stores the frozen scenes jsonb, the settings snapshot, the hash, the run, `restarted_after_edit` and `reused_segments`.
  - `render_segments` is the content-addressed cache, keyed by `(tenant_id, input_hash)`. `render_manifest_segments` records which cache entries each manifest pins.
  - `renders` holds the final MP4, the SRT, the preview asset, the sha256 and the QC report. `library_settings` holds the retention days and the last cleanup time.
  - The tenant-query lint covers the new tables.
- **FFmpeg runner** (`api/internal/media/ffmpeg`, extended from phase 7 and announced on the board):
  - A typed filtergraph builder: values are typed, filters come from an allowlist, and CUDA filters are refused.
  - Closed-GOP segment encodes (x264 or NVENC) with a keyframe at frame 0, no B-frames and a fixed timescale, so segments concat with `-c copy`.
  - `mov_text` subtitles, `+faststart`, a null muxer for measuring, and `ffprobe` helpers (streams, keyframe times, the encoder probe).
- **Render core** (`api/internal/render`):
  - Settings, their validation and their hash. The encoder probe (a one-frame NVENC test encode, else libx264).
  - A frame-exact timeline: each scene owns exactly its voice frames, and a crossfade borrows tail and head frames from its neighbours.
  - Motion graphs: static and Ken Burns (seeded per scene, eased, source pre-scaled 2×). Parallax is refused with "depth model not installed".
  - Manifest, segment, audio and subtitle hashes. SRT and ASS output (ASS text escaped), two-pass loudnorm.
- **Steps**: `render.scene_body`, `render.transition`, `render.audio_master`, `render.subtitles`, `render.compose` and `render.preview`.
  - Each step reads only its frozen manifest.
  - A cache hit is reused only when the object's stored `x-amz-checksum-sha256` equals `assets.sha256`. On a mismatch the asset row is deleted and the segment is encoded again.
  - Compose is resume-safe. It runs the concat demuxer with `-c copy`, muxes the audio and the soft subtitles, measures the QC report and uploads the final render.
  - Preview writes a 540p episode proxy and a proxy per scene.
- **Freeze** (`Freezer.Freeze`): the manifest and its pins are written in one transaction. No step is created for a hash that is already cached. Compose depends on every step, and preview depends on compose. Scenes that are not ready become reasons, and no placeholders are ever produced. `Freezer.Check` returns the readiness and the forecast without writing anything.
- **Supersede on edit** (`render.Superseder`): a 5 s debounce per episode and language on `scenes.Changed`. After it, a fresh manifest is frozen with `AfterEdit` and `SupersedeRun` is called. If the new freeze is not ready, the old run is canceled.
- **Disk guard** (`api/internal/diskguard`): `Watermark` is an `AdmissionCheck` that refuses render and model-pull enqueues below 40 GB free, and warns below 60 GB. It measures the tightest of several mounts, and fails closed when free space is unreadable. The API returns 507.
- **Library** (`api/internal/library`):
  - Listing with cursor paging, usage per project, and retention settings.
  - A dry-run preview whose token covers exactly the candidate set. Confirm answers 409 when the set changed.
  - A `library.cleanup` step, audited. The hourly scheduler runs `library.ttl_cleanup` for each tenant that is due once a day, and audits it too.
  - Nothing referenced by a selected take, a pinned manifest, a render, a character reference, a voice preset or a LoRA is deleted.
- **API** (openapi `paths/renders.yaml`, `paths/library.yaml`): render settings, render status (stages, reasons, disk, estimate, active run, latest restart), start (202/404/409/422/429/507), render list and item, library assets, usage, settings, cleanup preview and confirm.
- **Wiring**:
  - `cmd/api` runs the handlers, the disk guard, the Superseder and the library scheduler.
  - `cmd/worker` runs the render queue, the render and library handlers, and the disk guard, and its heartbeat publishes the encoder probe.
  - The compose files, `worker.Dockerfile` (ffprobe, and the Literata font with its OFL licence) and `.env.example` were updated to match.
- **Web**:
  - Render page: stage strip, settings panel ("No background music: narration only, by project decision.", parallax disabled), estimate, and the "Render episode" button, disabled with its reasons.
  - Live run counters over SSE, QC results with the preview player and downloads, and the restarted-after-edit banner.
  - The inspector shows the 540p preview of a scene.
  - Library page: virtualized cursor-paged table, usage, retention form, and a cleanup preview dialog that previews again automatically on a 409.
  - Library was added to the nav rail, the command palette and the top bar.
- **Tests added in the last session**:
  - `api/internal/integration/render_test.go`, with `render_helper_test.go`:
    - A fully generated episode (test doubles for image, voice and align) renders on the live worker at 640×360 libx264, with subtitles set to "both". The QC report is then checked: 1 video, 1 audio and 1 subtitle stream, duration within 1 frame, A/V drift ≤ 2 frames, −14 ±1 LUFS, true peak ≤ −0.5 dBTP, and no missing keyframes, missing scenes or placeholders.
    - A second start while a render is running gets 409.
    - A segment whose stored checksum was tampered with is encoded again by compose.
    - The unchanged re-render enqueues only compose and preview.
    - A render held in the queue is superseded by a scene motion edit. The new run encodes 1 body and at most 2 transitions, with no audio or subtitle step, and the status reports `restartedAfterEdit` with the reused count.
    - A freeze below a faked watermark is denied and leaves no manifest.
  - `api/internal/integration/library_cleanup_test.go`:
    - Listing and usage, and settings validation.
    - A confirm with nothing expired gets 422. A preview lists exactly the expired unselected take, and a stale token gets 409 after another take expires.
    - The confirmed cleanup removes the take rows, the asset rows and the objects, and writes one audit entry.
    - The daily scheduler enqueues `library.ttl_cleanup` for the due tenant, and it deletes the take that expired since, with its own audit entry.
  - `web/e2e/render-library.spec.ts`:
    - The render page of an episode without generated media shows the disabled button with its reasons, the no-music line, the disabled parallax option and the estimate. The storyboard link works.
    - The Library page shows usage, retention and assets, and opens and cancels the cleanup preview dialog. Screens go to `reports/phase-08-screens/`.
  - `deploy/compose.integration.yml` and `deploy/compose.ci.yml` set `DISK_MIN_FREE_GB=1` and `DISK_WARN_FREE_GB=2` on the api and the worker. Test runners have less free disk than the production watermark, and the tests fake a full disk themselves. This was announced on the board.

## Verification

| Check | Result |
|---|---|
| Host gen (migrations and models sync, redocly bundle, oapi-codegen, sqlc, web gen) | Generated-code drift: none (`git status` clean after gen) |
| Host lint | `go vet ./...` and `go vet -tags integration ./...` clean. golangci-lint 0 issues (default tags and the integration package). tools vet, tenantctx, the tenant-query lint and the models manifest lint pass. ruff clean |
| Host Go tests | `go test ./... -count=1` green (no `-race`: the host has no C compiler) |
| Python | ruff clean; pytest 66 passed, 2 skipped |
| Web | typecheck and lint clean; vitest 21 files, 105 tests passed. `vite build` and `budget-check` pass. The e2e spec passes eslint, and `tsc` on it reports only the missing `@types/node` that every existing spec also lacks |
| Earlier host smoke (sessions 2 and 3, not committed) | Real graphs on host ffmpeg 8.0.1: 2 scenes rendered frame-exact (156 frames = 5200 ms), a keyframe at every segment start, −14.0 LUFS integrated, 1 video + 1 audio + 1 subtitle stream. `measure()` and the preview trim graph worked |
| `scripts/tb.sh gen lint test` (toolbox, with `-race`) | **pending: Docker down** |
| Integration (`loomtale-a`, heavy lock) including the new render and library tests | **pending: Docker down** |
| Playwright e2e (`--workers=1`, heavy lock) including `render-library.spec.ts` | **pending: Docker down** |

## Success criteria

| Criterion | Status |
|---|---|
| A fixture episode renders to a playable MP4 with narration, soft or burned subtitles and a QC report, and rerendering one scene touches only that scene's segments (AC4) | Built and covered by `TestRenderEpisodeCacheReuseAndRestartAfterEdit`; **pending: Docker down**. The fixture renders at 640×360 for speed. A 1080p fixture render belongs to the Docker run |
| An edit during a render produces a superseded run and a consistent new render | Built and covered by the same test; **pending: Docker down** |
| The zoompan gate result is recorded and the 60-min budget holds with the chosen path | **pending: Docker down** (it needs the worker image on the VM). The 60-min NVENC budget is measured on the real GPU in phases 9c/12 by design |
| The render page and Library match the wireframes' functional zones, and "No background music" is stated | Built. Vitest covers it, and `render-library.spec.ts` covers it live; **pending: Docker down** |
| GPU check: `ENCODER=nvenc` records `encoder: h264_nvenc` without evicting a resident model | **pending: Docker down**. Likely also blocked by the static ffmpeg (see below) |

## Deviations

1. **Workers download inputs instead of reading presigned URLs.** They download through `storage.Internal` into a 0700 temp dir pinned to the object version, as phase 7 does. No presigned URL reaches ffmpeg, so there is no presign TTL in the render package.
2. **Burned subtitles are cut per segment on each scene's own clock.** The body hash uses the align asset ids, not the cue text, so freeze needs no storage reads and a scene's segments stay cached when earlier scenes change length.
3. **Starting a render while one is running answers 409.** Supersede happens only on scene edits, never from the button.
4. **A cache hit creates no step.** Compose runs the cache check again for every entry and re-encodes a vanished or tampered one inline. That is why the tampered-segment test sees the re-encode inside compose.
5. **The renderscratch volume holds the worker temp dirs** (`TMPDIR=/scratch`), because a compose can hold GBs of segments and a RAM tmpfs cannot.
6. **No test kills the worker during compose.** The integration tests run in a toolbox container without the Docker socket. Compose is resume-safe by design (`GetRenderByManifest`, fresh final keys per upload), and the kill test stays a manual check for the Docker run.

## Follow-ups

- Once Docker is back, run the whole verification under the heavy lock: `scripts/tb.sh gen lint test`, then integration and e2e with compose project `loomtale-a`, then the zoompan gate. Record the numbers here.
- Phase 10 must extend `ListExpiredSegments` and `ListExpiredTakes` with publications.
- The mwader static ffmpeg probably cannot load NVENC, so the probe would fall back to libx264. The GPU override may need a dynamic ffmpeg build.
- `Prober.KeyframeTimes` has a 30 s timeout, which may be tight for a 60-min episode. Check it during the long render.
- Per-scene previews decode the proxy from its start each time. This is quadratic on long episodes; revisit if it is slow.
- `PutFileChecksummed` is a single PUT, so objects are limited to 5 GiB.
- The library tests could flake if the api's hourly TTL scheduler fires for the test tenant between a preview and its confirm (the token would then be refused). This is rare, and noted here.

## Review

The first independent review (plans/reports/code-reviewer-260927-0148-phase-08-render-pipeline-library-review.md) found two blocking test defects. Both are fixed on the branch and were re-run live under the heavy lock on compose project `loomtale-a`.

- The integration engine tests put their fake kinds on the render queue, which the live worker now serves, so the worker claimed them and `TestInteractiveStepWaitsAtMostOneChunkBehindBatch` never saw its interactive step start. Fixed: `testQueue` in `api/internal/integration/pipeline_helper_test.go` is now the io queue, which the integration stack's worker never enables (only a GPU worker with a models directory does), and the comment explains why the queue cannot be a made-up name (the `pipeline_steps.queue` CHECK constraint). Outcome: integration 91 passed, 0 failed, including that test and the render tests.
- `web/e2e/render-library.spec.ts` asserted `toBeDisabled()` on the parallax option, which Playwright reports as enabled inside a label-wrapped select. Fixed: the spec asserts `toHaveAttribute("disabled", "")`. Outcome: the render and library spec passes, including its Library half. Playwright ran 5 passed and 1 failed; the failure is `youtube-settings.spec.ts` (two headings named "YouTube channels"), which comes from main and which lane h has already raised on the board for lane c.

The second independent review (plans/reports/code-reviewer-260927-0250-phase-08-render-pipeline-library-round2-review.md), run after main (with the analytics spec) was merged in, found one blocking item. It is fixed on the branch and was re-run live under the heavy lock on compose project `loomtale-a`.

- With seven spec files each signing in as the seeded owner, the sixth login in a run hit the per-account login limit (a burst of 5, then one every 12 s) and got 429, so `writer-import-settings.spec.ts` timed out. The limiter is a security control and was not changed. Fixed in the e2e harness: `web/e2e/global-setup.ts` signs the owner in once per run and saves the session to `web/playwright/.auth/owner.json` (gitignored), `web/e2e/owner-session.ts` holds the owner credentials and that path, and every spec except the smoke spec starts from that session instead of its own login. The first live run showed a second constraint: a login revokes the account's other sessions, so the smoke spec (which tests the login form and logout) ended the shared session for the specs after it. `web/playwright.config.ts` therefore has two projects, `signed-in` for all other specs and `login-form` for the smoke spec, which depends on `signed-in` and so always runs last. Running last, the smoke spec's `getByText("GPU")` also matched a queued "Waiting for GPU slot" job on the dashboard, so it now matches the widget label exactly. Outcome: three full Playwright runs on fresh stacks passed 7 of 7 each, with exactly two login requests per run, both 200. A run now uses two logins however many spec files are added. If a signed-in spec fails, Playwright skips the smoke spec in that run and reports it as not run.

The review's Medium items (no `h264_nvenc` in the static ffmpeg, the StartRender check-then-freeze race, no sweep of orphaned /scratch temp dirs, and migration ordering) and the second review's new Medium (a restart supersedes an active run even when the fresh manifest hash equals the active one) were outside these fix rounds and stay open. The review confirmed that MinIO returns the checksum on read-back, which answers the first unresolved question below.

## Unresolved questions

- (Answered by the review: yes.) Does MinIO return the header-supplied `x-amz-checksum-sha256` from `StatObject(Checksum: true)` for a single PUT? The cache check and the tampered-segment test depend on it. The integration run will tell.
- The review found that the static ffmpeg has no `h264_nvenc`. Does the GPU override need a dynamic ffmpeg build, or should the NVENC criterion be dropped?
