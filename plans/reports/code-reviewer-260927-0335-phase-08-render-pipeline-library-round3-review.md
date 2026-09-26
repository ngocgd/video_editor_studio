# Code review: phase 8, render pipeline and library (independent verify, round 3)

Branch `feat/render-pipeline-library`, worktree `.claude/worktrees/lane-a-8`, head `debad05` before this round. I merged main `217ef27` (the character LoRA and scene scoring merge) into the branch as `420bcaa`. The merge is local only and has not been pushed. The only conflicts were in two generated files, `api/internal/db/gen/models.go` and `api/internal/httpapi/gen/server.gen.go`. I took main's side of both and regenerated them with `scripts/tb.sh gen`. No hand-written file conflicted.

I then reviewed `git diff main...HEAD`: 136 files, about 20,000 added lines. I read the areas with the most risk in full: supersede and restart, `StartRender`, the segment cache and the MinIO checksum read-back, the library cleanup queries, the disk watermark, the authz roles in the OpenAPI spec, ASS escaping, the worker Dockerfile and the new e2e harness. I also checked where main's newly merged scoring and depth steps meet this branch.

Verdict: **PASS**. No Critical or High findings remain. Every check is green, including two full Playwright runs. The four success criteria of the phase file are met. The NVENC part of the manual GPU check is not: the worker's ffmpeg has no NVENC (M1). The phase's risk table accepts that case with the libx264 fallback, and the 60-min budget holds on the CPU path. The resident-model half of that check is pending because the user has paused model downloads. Five Medium findings stay open, and none of them blocks the merge.

## Verification

| Check | Result |
|---|---|
| `scripts/tb.sh gen` twice, then host `git status --porcelain` | Clean after the second run: no drift in the generated code. |
| `scripts/tb.sh lint test` (`-race`) | Green. golangci-lint reports 0 issues, and ruff and eslint are clean. 54 Go packages pass with 0 FAIL, and pytest has 108 passed and 5 skipped. |
| Web (`npm ci`, `typecheck`, `lint`, `npm test`, `vite build`, `budget-check`) | Green. Vitest has 140 tests passing, and the bundle budget check passes. The route tree is unchanged. |
| Integration (`loomtale-a`, heavy lock `a-p8v3`, `compose.integration.yml`) | **92 top-level tests passed, 0 failed, 0 skipped** (`ok loomtale/api/internal/integration 63.7s`). This includes `TestRenderEpisodeCacheReuseAndRestartAfterEdit`, `TestLibraryCleanupPreviewConfirmAndDailyTTL`, `TestInteractiveStepWaitsAtMostOneChunkBehindBatch` and main's new `seed_llm_default` test. |
| Playwright run 1 (`compose.yml`, default limits, fresh stack, `--workers=1`) | **7 passed.** The API log has 2 `auth/login` lines and 0 responses of 429. |
| Playwright run 2 (fresh stack) | **7 passed**, with 2 logins and 0 responses of 429. Round 2's High H1, the per-user login bucket, is fixed. |
| NVENC probe (`loomtale/worker:local`, `--gpus all`) | `-encoders` lists only `libx264` and `libx264rgb`. A one-frame `h264_nvenc` encode fails with "Encoder not found" (M1). |
| Zoompan gate | Not re-run. The render, media and Dockerfile code has not changed since round 2 measured about 257 fps for two concurrent 1080p x264 encodes. At that rate a 60-min episode's bodies take about 7 min, within the 15-min limit. |

Every stack was torn down with `down -v`, and no `loomtale-a` container is left running. The Playwright screenshot churn under `plans/` was reverted. My first NVENC probe inside the heavy script failed because Git Bash rewrote the entrypoint path. I re-ran it under the lock with `MSYS_NO_PATHCONV=1`, and the table shows that result.

## Board

The board has no open ASK addressed to lane a. The ASKs from lane g about `render-library.spec.ts:68` and the integration test queue were answered, and the fixes are on the branch. The e2e harness change (sign in once in `globalSetup`, with `storageState`) was announced in lane a's 03:18 DONE line. Lane g's `feat/review-publish` builds on this branch and adds specs, so those specs must not sign in themselves. Lane h's 01:53 SSE change and 03:08 `listRunSteps` 404 change are on lane h's branch, not on main, and do not touch this branch's files.

## Findings

### Critical / High

None. Round 2's H1 (the e2e suite exceeding the per-user login bucket) is fixed:

- `web/e2e/global-setup.ts` signs the owner in once and saves the session to `web/playwright/.auth/owner.json`, which is gitignored.
- Only `smoke.spec.ts` still signs in. It runs last in its own `login-form` project, because a login revokes the account's other sessions.
- The limiter is unchanged. A grep confirms that no other spec signs in.

### Medium

**M1. The worker's ffmpeg has no NVENC (carried, re-confirmed live).** `deploy/docker/worker.Dockerfile` copies the static `mwader/static-ffmpeg` build, whose encoder list has no `h264_nvenc`. As a result, `encoder: auto` always resolves to libx264.

The phase file's "Modify" list asks for "FFmpeg with nvenc", so this is a gap against the file. However, the phase's risk table plans for exactly this case ("NVENC missing in the WSL2 container: the probe falls back to x264; the UI shows the encoder"), and the CPU path meets the 60-min budget.

This answers the cook report's open question: yes, the GPU override needs a dynamic ffmpeg build that can load the NVIDIA libraries. That is a follow-up for the lead, either on this branch before the GPU budget runs or in phase 12. It does not block the merge.

**M2. Two concurrent starts can create two render runs (carried).** In `api/internal/render/http.go:244`, `StartRender` checks `GetActiveRenderRun` and then calls `Freezer.Freeze`, with no lock in between. `Freeze` also links the run to its manifest only after it enqueues the run.

The damage is bounded. `renders` has a unique `manifest_id` and upserts on it, and segments are content-addressed. So the worst case is a duplicate render and wasted encode time, not corrupted output. A per-episode and language advisory lock around the check and the freeze would close both windows.

**M3. Step temp dirs on `/scratch` are not swept after a worker crash (carried).** In `api/internal/render/inputs.go:22`, a step's temp dir is removed only by that step's own deferred cleanup. The disk watermark includes `/scratch`, so a leak ends in refused enqueues rather than a full disk.

**M4. Migration ordering (carried, cross-branch).** `20260927100000_renders.sql` sorts before main's `20260927400000`, `…500000` and `…600000`, which is inside the range the lead assigned. `api/cmd/loomtale/migrate.go:53` runs `goose.UpContext` without `WithAllowMissing`, so a database that has already applied main's later versions refuses this one. Main already has the same problem with phase 7's lower versions, so the decision belongs to the lead.

**M5. A debounced restart supersedes a run whose manifest did not change (carried).** In `api/internal/render/supersede.go:120-160`, `Restart` freezes a new manifest and supersedes the active run without comparing hashes. `GetActiveRenderRun` already returns the active manifest's hash.

Two cases trigger it:

- a render started within 5 s after an edit;
- a `scenes.Changed` event that leaves the render inputs unchanged.

In both, in-flight encodes are canceled and redone, and the page says "Render restarted after edit". Correctness holds, and the cached segments are reused.

### Low

- **L1–L4 (carried).**
  - A final render asset can be orphaned if the worker dies between the upload and `InsertRender`.
  - `DeleteLibraryAssets` re-checks only selected takes and renders. Its candidate queries also exclude character references, voice presets, LoRAs, voice previews and pinned manifests, and all of that runs in the same step, so the race window is milliseconds. I checked that character references never reuse a take's asset: both `CreateCharacterRef` call sites store their own asset.
  - `RemoveAllVersions` runs after the row delete.
  - `cmd/api` builds the `Freezer` without `Depth`.
- **L5 (carried).** The tests are thinner than the phase's Tests list:
  - 3 scenes instead of 12;
  - "1–2 transitions" instead of the exact pair;
  - no worker-kill-during-compose test.

  Compose resume relies on the pipeline's step retry and on `renders` upserting by `manifest_id`, but no test exercises it.
- **L6 (new, with main's scoring and depth steps).**
  - Parallax: `MotionAvailable` answers "parallax rendering is not built yet" even when `/gpu` reports depth, and `motionFilters` (used by `BodyGraph`) refuses it, so the two-layer graph still has to be written. The phase file hands "Phase 9c enables parallax" onward, and the depth engine cannot run while model downloads are paused. So this is a follow-up, not a gap in this phase.
  - Scores: QC reads `scene_takes.params.score`, which is exactly the key main's `image.score` step writes (`scenes.ParamScore = "score"`, a float). The pact holds.
  - Depth maps: the depth maps that main writes, `params.depthAssetId`, are separate assets that no library cleanup query knows about. Cleaning up a take therefore leaves its depth map orphaned, which is a small storage leak with no data loss.

### Checked and fine

- **Authorization.** `x-min-role` is viewer on all six read operations. It is editor on `putRenderSettings`, `startRender`, `putLibrarySettings`, `previewLibraryCleanup` and `confirmLibraryCleanup`. That matches "Cleanup is owner/editor only". Retention is range-checked to 1–365 days in both the spec and `Service.UpdateSettings`.
- **Tenant isolation.** Every query in `renders.sql`, `manifests.sql` and `library.sql` filters by `tenant_id`, and every join also matches `tenant_id`. `lint-tenant-queries` and tenantctx pass.
- **Segment cache integrity.** `PutFileChecksummed` sends `X-Amz-Checksum-Sha256`. That is an amz header, so minio-go passes it through unprefixed, and MinIO verifies it against the body. `ObjectSHA256` reads it back through `StatObject(Checksum: true)` pinned to the version ID. An empty or unreadable checksum never matches, so the segment is re-encoded (fail-safe). The integration run's cached re-render and its tampered-segment case confirm this live, which answers the cook report's MinIO read-back question.
- **Disk watermark.** It guards `render` runs, `render.*` steps and `models.pull`. `StartRender` and `CreateRun` map admission denial to 507. The API measures the MinIO volume, mounted read-only, and the host drive. The worker measures `/scratch` and the host drive.
- **Injection.** FFmpeg runs only through the typed argv builders. `EscapeASS` maps `\`, `{` and `}` to full-width characters, which also neutralises `\N` and `\h`. Newlines are split into lines, and `-->` is rewritten in SRT. The web features use no `dangerouslySetInnerHTML`.
- **Secrets.** The e2e owner password is the documented demo seed and can be overridden with `LT_E2E_PASSWORD`. The saved session file is gitignored. No secret appears in the diff.

## Success criteria

| Criterion | Status |
|---|---|
| A fixture episode renders to a playable 1080p MP4 with narration, subtitles and a QC report, and rerendering one scene touches only that scene's segments | Met. Integration passes this round. Round 2 verified the 1080p variant, and the render code is unchanged since then. |
| An edit during a render produces a superseded run and a consistent new render | Met (integration). See M5 for a restart when nothing changed. |
| The zoompan gate result is recorded and the 60-min budget holds with the chosen path | Met on the CPU path. The round 2 measurement is about 7 min per 60-min episode against a 15-min limit, and the code is unchanged. |
| The render page and Library match the wireframes' functional zones, and "No background music" is stated | Met. `render-library.spec.ts` passed in both Playwright runs, and vitest covers both pages. |
| GPU check: `ENCODER=nvenc` records `h264_nvenc` without evicting a resident model | NVENC part not met: the worker's ffmpeg has no NVENC (M1), and the fallback the phase's risk table accepts is in place. The resident-model part is pending because model downloads were paused by the user. |

## Unresolved questions

- Should NVENC be delivered by a dynamic ffmpeg in the GPU worker image before phase 12 measures the GPU budget, or should that criterion move to phase 12 (M1)?
- Migration ordering is still with the lead: renumber above main's maximum, or run goose with `WithAllowMissing` (M4)?
- Should M2 and M5 (the start race and the restart when nothing changed) be fixed before lane g's review and publish branch builds on renders, or tracked as follow-ups?
- Is a manual worker-kill-during-compose check with a longer fixture still wanted before phase 12 (L5)?
