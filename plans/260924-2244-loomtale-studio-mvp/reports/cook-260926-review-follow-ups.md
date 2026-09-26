# Review follow-ups from the merged phases

Branch: `fix/review-follow-ups` · Worktree: `.claude/worktrees/lane-d-fixes` · Base: `main` @ `4962ecd`
Status: DONE_WITH_CONCERNS. Every fix in scope is built, has a regression test, and passes generation, lint and unit tests on the host toolchain. The toolbox run (`scripts/tb.sh gen lint test`), the integration suite and the Playwright e2e suite are still pending because Docker Desktop's engine answers HTTP 500: its data disk `docker_data.vhdx` is still attached to Windows (checked with `Get-DiskImage` at the end of this run).

## What shipped

### Model downloads
- A pull that hits its deadline no longer stays `downloading` until the owner pauses it. Only `context.Canceled` (owner pause or worker stop) counts as a pause. A deadline is a failed attempt that stays transient, so the last attempt leaves the row `failed`, and the owner can resume it with Install (`pullOutcome` in `api/internal/models/steps.go`). The `models.pull` step now has a 3h timeout override, which is below the 4h rescue window (`api/internal/pipeline/worker.go`).
- Concurrent pulls of the same file no longer race on a shared staging file. `downloadExclusive` takes a per-checksum lock (a flock on `.staging/<sha>.lock` on Unix, an in-process lock on Windows), checks the file again, then downloads and verifies it before it releases the lock. Resume-from-partial still works across pulls, and a shared multi-GB file is downloaded once.
- Tests: `steps_test.go` (pause vs deadline outcomes), `download_test.go` (`TestConcurrentInstallsOfOneFileDownloadItOnce`, `TestStagingLockWaitEndsWithTheContext`).

### Worker transfer errors
- `transfer_status()` in `workers-python/src/loomtale_worker/servicers/streaming.py` maps presigned GET/PUT responses as follows: 403, 408 and 429 become `UNAVAILABLE` (retryable, so an expired URL is presigned again), other 4xx become `INVALID_ARGUMENT`, and 5xx become `UNAVAILABLE`. Both `fetch_input` and `push_output` use it.
- Tests: `workers-python/tests/test_media_servicers.py`, plus `api/internal/providers/workerconn/dial_test.go`, which pins the Go-side retry classification of these codes.

### Integration suite login budget
- One helper, `isolateLoginIPBudget(t)` in `api/internal/integration/session_helper_test.go`, clears the per-IP login buckets before a test and again in its cleanup. `login()` and the two tests that post raw `/auth/login` requests use it. The per-test refund helper was removed, and `zz_login_rate_limit_test.go` became `login_rate_limit_test.go` because test order no longer matters.
- The CLI-status test's provider probe now uses a 45s client timeout, which is above the server's 30s `testCallTimeout`.

### Story, drafts and settings
- The tenant-query lint now covers the story tables, `llm_settings` and `draft_step_applications`. `TrimDraftRevisions` filters by `tenant_id`.
- Each AI step is applied to a draft at most once. Migration `20260927400000_draft_step_applications.sql` adds a claim table. `commitDraftWrite` (`api/internal/story/draft_write.go`) runs the claim, the version-fenced update, the revision insert and the trim in one transaction. Conflicts return 409 with the titles "version conflict", "draft too large" (more than 5000 paragraphs) or "already applied".
- The OpenAPI draft ops have length and item bounds: paragraph ids are 1..64 characters, text is at most 20000 characters, and each id list holds at most 5000 items.
- The writer ignores a second accept (Tab or click) while the first one is in flight.
- The LLM settings Test endpoint returns `latencyMs` for both successful and failed probes.
- Outline numbering: each outline step gets its episode number when the series is generated. Outlines after the first 20 run at batch priority. If the episode insert hits a number collision, it is retried with the next free number, and the LLM is not called again (`outline_episode.go`).
- The bible seed schema stores the glossary as the `{termZh, en, vi}` term array that the editor reads. The bible editor shows all six sections, and a reload or save resets a section's draft because each editor is keyed by the section version. A glossary that is not an array is edited as text, with a notice, so nothing is dropped.
- The reply token cap for streamed episode actions and translation is sized from the target text: 1.5 tokens per rune plus 512, with a floor of 4000 and a ceiling of 16384.

### Importer
- A GB18030 fallback is accepted only when the decoded text is at least half Han. Western mojibake now fails as an undetected encoding.
- Text before the first chapter heading becomes a "Preface" chapter instead of being dropped, so later chapter indexes shift by one.
- Rune offsets use one running counter.

### Formatting
- `gofmt` was applied to `api/cmd/loomtale/migrate.go`, `api/internal/bench/harness_test.go`, `api/internal/pipeline/worker.go`, `api/internal/story/ai_actions.go` and `api/internal/integration/auth_flow_test.go`. `api/cmd/worker/config.go` was already gofmt-clean on main.

## Verification

Host toolchain (Go 1.26.8, golangci-lint 2.14.0, node 22, scratch Python venv), run at the branch head in this session:

| Check | Result |
|---|---|
| gen by hand (migrations/models sync, redocly 1.25.11 bundle, oapi-codegen, sqlc, `web npm run gen`) then `git status` | no drift |
| `go vet ./...` and `go vet -tags integration ./...` (api, tools) | clean |
| `golangci-lint run` with and without `--build-tags integration` (api, tools) | 0 issues |
| tenantctx, `scripts/lint-tenant-queries.sh`, models lint | OK |
| `go test ./... -count=1` (api, tools; no `-race`, because the host has no C compiler) | all pass |
| pyworker `ruff check .` and `pytest -q` | clean; 72 passed, 2 skipped |
| web typecheck, lint, `npm test`, `npx vite build`, `npm run budget-check` | pass (81 tests); budget passed |

Pending because Docker is down:
- `scripts/tb.sh gen lint test`: this checks byte-identity with the toolbox and runs the tests with `-race`.
- The integration suite, which is the only place the login-budget helper, the apply-once claim (`TestAcceptingAnInsertingStepTwiceInsertsItOnce`), the oversized-paragraph rejection and the 45s probe timeout actually run. They compile and pass vet and lint with the `integration` tag.
- Playwright e2e.

## Observable criteria
- A timed-out pull ends in `failed` and can be resumed, and two concurrent pulls of one file download it once. Both are shown by unit tests with a fake hub. A live check with real weights is pending because model downloads are paused by the user.
- An expired output URL is retried: shown by pyworker unit tests. A live GPU run is pending (Docker down, and model downloads paused by the user).
- The integration suite no longer hits 429: pending the integration run (Docker down).

## Deviations
- On Windows the staging lock only works within one process, because there is no flock. The deployment runs on Linux containers, where the flock holds across processes.
- A 403 that comes from a bad signature, rather than an expired URL, is now retried a bounded number of times before it fails. Wasting those retries is acceptable because the worker cannot tell the two cases apart.

## Out of scope and left to others
- These items need product decisions or model weights: deployment-wide model auth, voice calibration (needs the phase 7 voice presets), extras lock advisories (need downloads), and two phase 6 spec deviations for the lead.
- `gofmt -l` still lists files owned by other lanes: `assetsapi/finalize.go`, `pipeline/types.go`, `pipelineapi/audit.go`, `providers/bootstrap/bootstrap.go`, `integration/pipeline_gpu_test.go` and `workerpb/roundtrip_integration_test.go`. They were left alone to avoid merge conflicts.
- Merge note: `scripts/lint-tenant-queries.sh` `TENANT_TABLES` is also edited on lanes a (phase 7) and c (YouTube). The second merger takes the union of the lists. Generated code is regenerated, never hand-merged.

## Unresolved questions
- When will Docker Desktop's data disk be detached so the toolbox, integration and e2e runs can happen? This branch should not be merged until they pass.
- Should the "Preface" chapter title be localised with the draft language instead of always being English?
