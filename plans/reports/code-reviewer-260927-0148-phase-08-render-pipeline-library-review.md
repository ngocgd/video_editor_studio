# Code review: phase 8, render pipeline and library (independent verify, round 1)

Branch `feat/render-pipeline-library`, worktree `.claude/worktrees/lane-a-8`. Reviewed `git diff main...HEAD` after merging main `c05667d` into the branch as `406d6f1` (local only, not pushed). The merge had conflicts. In `api/cmd/api/config.go` and `server.go` both sides were kept: the disk guard config next to the Google OAuth and YouTube quota config, and `RenderAPI` and `LibraryAPI` next to `ChannelsAPI`. The `TENANT_TABLES` list in `scripts/lint-tenant-queries.sh` became the union of both sides. The command palette icons are now the union too (`Library` and `Video`). Generated code was regenerated with `tb.sh gen`, and a second gen run produced no drift.

Verdict: **FAIL**. There are two High findings, both test defects that turn the integration suite and Playwright red. The product code behaved correctly in every live check I ran.

## Verification

| Check | Result |
|---|---|
| `scripts/tb.sh gen` (twice) | No drift on the second run. The work tree is clean after gen. |
| `scripts/tb.sh lint test` (with `-race`) | Green. golangci-lint reports 0 issues, ruff is clean, go test passes in api and tools, and pytest has 108 passed and 5 skipped. |
| Web (`typecheck`, `lint`, `vitest`, `vite build`, `budget-check`) | Green. 23 test files and 121 tests passed, and the budget check passes. `routeTree.gen.ts` is unchanged. |
| Integration (`loomtale-a`, heavy lock, `compose.integration.yml`) | **90 passed, 1 failed**: `TestInteractiveStepWaitsAtMostOneChunkBehindBatch` (see H1). `TestRenderEpisodeCacheReuseAndRestartAfterEdit` and `TestLibraryCleanupPreviewConfirmAndDailyTTL` pass. |
| 1080p variant of the render test (settings temporarily set to 1920×1080 with 42 px subtitles, then reverted) | PASS in 10.9 s. The QC checks pass: streams, duration ±1 frame, A/V drift, −14 LUFS ±1, true peak and keyframes. |
| Playwright (`compose.yml`, default limits, `--workers=1`) | **5 passed, 1 failed**: `render-library.spec.ts:68` (see H2). The API logged 0 responses of 429. |
| Zoompan gate (worker image ffmpeg 7.1.1, the exact `BodyGraph` Ken Burns chain, `--cpus 8 -m 3g`) | One 60 s 1080p30 segment with x264 veryfast crf 20 took 11 s (about 164 fps). Two concurrent encodes took 15 s for 3,600 frames (about 240 fps together). Filter only, without the encoder, two concurrent took 11 s (about 327 fps). |
| NVENC probe (worker ffmpeg, `--gpus all`, `NVIDIA_DRIVER_CAPABILITIES=compute,video,utility`) | **`Unknown encoder 'h264_nvenc'`**: the static build has no NVENC compiled in (see M1). |
| MinIO checksum read-back (pinned bitnamilegacy/minio image, versioned bucket, `PutFileChecksummed` then `ObjectSHA256`, run as a throwaway probe test and deleted afterwards) | The checksum is read back and equals the one uploaded. This answers the cook report's open question: yes. |
| Episode delete with renders (Postgres 17 probe of the RESTRICT FK from `renders` to the manifest, both cascading from `episodes`) | The cascade succeeds and leaves no renders behind. The RESTRICT does not block deleting an episode. |

Stacks were torn down with `down -v`, and no `loomtale-a` containers or volumes remain. The Playwright screenshot churn under `plans/` was reverted.

## Findings

### High

**H1. The integration test queue is now served by the live worker.** `api/internal/integration/pipeline_helper_test.go:239` has `const testQueue = pipeline.QueueRender`. Its comment says the live worker "never serves render". This branch makes the worker serve `QueueRender` (`api/cmd/worker/main.go:216`, where the worker log shows `queues_enabled:3`). The live worker therefore takes the tests' fake-kind jobs and snoozes them. `TestInteractiveStepWaitsAtMostOneChunkBehindBatch` fails with "interactive step never started", which is the failure lane g reported. The worker log also shows six "re-enqueued a step found queued with no live job" warnings during the suite. Other tests that use `testQueue` pass only by timing.
Fix: use a queue name that only the tests register (for example `integration_test`), and add it to the tests' own River clients. Then correct the comment.

**H2. The render and library e2e spec fails on a disabled `<option>`.** `web/e2e/render-library.spec.ts:68`: `expect(getByRole('option', {name: 'Parallax (depth model not installed)'})).toBeDisabled()` receives "enabled", although the element is `<option disabled value="parallax">`. Playwright reports this for a select wrapped in a `<label>`, as lane g found. The page itself is correct. Because the spec stops at line 68, the Library half never ran live: usage, retention, assets, and the cleanup preview dialog.
Fix: assert `toHaveAttribute('disabled', '')`. Rerun Playwright and confirm the Library section passes.

### Medium

**M1. The worker's static ffmpeg has no NVENC.** The pinned `mwader/static-ffmpeg` 7.1.1 answers `Unknown encoder 'h264_nvenc'` even with the GPU and the video driver capability passed through. `encoder: auto` therefore always resolves to libx264, and `h264_nvenc` always gives the not-ready reason. The GPU check ("`ENCODER=nvenc` records `encoder: h264_nvenc`") cannot pass with this image. This is a build decision, not an external blocker: an NVENC-capable ffmpeg is needed for the GPU override (for example a glibc build with nvenc on a CUDA base image, or a separate GPU worker image). The CPU path still meets the budget (see criteria), so this is Medium.

**M2. Two concurrent starts can create two render runs.** `RenderAPI.StartRender` (`api/internal/render/http.go:244`) checks `GetActiveRenderRun` and then calls `Freezer.Freeze` with no lock and no unique constraint. Two concurrent `POST /episodes/{id}/renders` requests, such as a double submit or two tabs, can both pass the check. That creates two active render runs for the same episode and language. It wastes encode time, and the Superseder only tracks the newest run. Fix: take a transaction-scoped advisory lock on (tenant, episode, lang) around the check, the freeze and the enqueue, or use a partial unique index.

**M3. Step temp dirs on the render scratch volume leak after a worker crash.** `newTempDir` (`api/internal/render/inputs.go:22`) is removed only by the step's own deferred cleanup. After an OOM kill or restart during compose, which can hold GBs of segments, the dirs stay on `renderscratch` forever. Nothing sweeps `/scratch` at worker start, and `/scratch` counts toward the disk watermark. Fix: remove stale `render-*` dirs at worker start (no step runs yet at that point), or sweep them by age.

**M4. Migration ordering, carried over from phase 7 and still awaiting the lead.** `20260927100000_renders.sql` sorts before main's `20260927400000_draft_step_applications.sql`. goose refuses to migrate a database that already applied the later version. Fresh volumes are fine. Decide between renumbering and `WithAllowMissing` before merge.

### Low

- **L1.** Compose can orphan the final render when the worker dies after `PutFileChecksummed`/`CreateDerivedAsset` but before `InsertRender`. Resume creates a fresh key, and the orphaned asset is never a cleanup candidate, because cleanup only considers segments and takes.
- **L2.** `DeleteLibraryAssets` re-checks only selected takes and renders. Character references, voice presets, LoRAs and pinned manifests are checked only by the list query that runs just before it. The window is milliseconds, but an asset delete cascades into `character_refs`.
- **L3.** Library cleanup's `RemoveAllVersions` runs after the row delete. It can drop a version that a concurrent step re-uploaded under the same content-addressed key. This self-heals, because the next lookup fails its checksum and encodes again.
- **L4.** `cmd/api` builds the `Freezer` without `Depth`, so parallax stays disabled until phase 9c part 2 wires `/gpu` capabilities. This is expected by the pact and noted here only.
- **L5.** The tests are thinner than the phase's Tests list. The fixture has 3 scenes, not 12, and the scene edit asserts "1 body and 1–2 transitions" rather than naming the exact transitions. Killing the worker during compose is not tested (deviation 6). The 1080p fixture passed only in this verifier's variant run.

### Checked and fine

- Authz: every new operation has an `x-min-role`. Reads are `viewer`. Settings, start, cleanup preview and cleanup confirm are `editor`.
- Tenant scoping: every new query is filtered by tenant, and the tenant lint passes. Steps load the manifest by (tenant, scope id) and check the scope. `IsTenantKey` guards object deletes. `RemoveAllVersions` deletes the exact key only.
- Injection: filters come from an allowlist and values are typed. `Expr`, `File` and `Dir` are validated, and CUDA filters are refused. ASS text is control-stripped with braces and backslashes swapped. SRT `-->` is neutralised.
- Cleanup: confirm recomputes the preview server-side and compares the token, answering 409 on a mismatch. The step lists again and deletes only candidates that are still expired. An unaudited manual cleanup does not run.
- Disk guard: it guards `render.*` and `models.pull`, fails closed when it cannot measure free space, and returns 507 on the API.

## Success criteria

| Criterion | Status |
|---|---|
| A fixture episode renders to a playable 1080p MP4 with narration, subtitles and a QC report, and rerendering one scene touches only that scene's segments | Met. Integration passes at 640×360 and the verifier's 1080p variant passes. The cache is checksum-verified, and read-back is confirmed on MinIO. |
| An edit during a render produces a superseded run and a consistent new render | Met (integration). |
| The zoompan gate result is recorded and the 60-min budget holds with the chosen path | Met on the CPU path. With 2 concurrent x264 encodes at about 240 fps together, the 108,000 frames of a 60-min episode take about 7.5 min, within the 15 min limit. This was measured on the dev host inside the worker's 8-CPU/3 GB limits, not on the 12-vCPU VM, and on a testsrc2 still. The 2× pre-scale zoompan path is kept. |
| The render page and Library match the wireframes' functional zones, and "No background music" is stated | Failed live because Playwright is red (H2). Vitest covers both, and the render page shows the text, but the Library half never ran. |
| GPU check: `ENCODER=nvenc` records `h264_nvenc` without evicting a resident model | Failed: there is no NVENC in the worker's ffmpeg (M1). The resident-model part is pending because model downloads were paused by the user. |

## Unresolved questions

- Which ffmpeg build should the GPU override use for NVENC, given that the static musl build cannot load the NVIDIA libraries?
- Should the migration be renumbered above main's maximum, or should goose run with `WithAllowMissing`? The lead has not decided yet (M4).
- Is a manual worker-kill-during-compose check still required before merge, or is it deferred to phase 12?
