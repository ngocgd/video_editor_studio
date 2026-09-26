# Independent review: review follow-ups (round 1)

Branch: `fix/review-follow-ups` @ `d866b89` · Base: `main` @ `4962ecd` (the branch already contains main, so no merge was needed) · Reviewer: lane d verifier
Verdict: PASS. There are no Critical or High findings. Every check is green on the toolbox and on Docker. The only open criteria are the ones that need model weights, which the user has paused.

## Scope reviewed
I reviewed `git diff main...HEAD`: 55 files, +1750/-363 lines. The generated code was checked by regenerating it, not by reading it. The diff was compared with the scope in the cook report `plans/260924-2244-loomtale-studio-mvp/reports/cook-260926-review-follow-ups.md` and with the board. I found nothing outside that scope. The board has no CHANGE lines from other lanes that touch these files, apart from `TENANT_TABLES` in `scripts/lint-tenant-queries.sh` (the second merger takes the union). No other branch calls the removed helpers `resetLoginIPBucket` or `refundLoginIPBudget` outside files that main already has, so later merges will pick up `isolateLoginIPBudget` without a compile break.

## Findings

### Critical / High
None.

### Medium
1. **The new `ParagraphOp.text` limit of 20000 characters can lock a user out of editing a long paragraph that already exists.** Imports and AI results split paragraphs only on blank lines (`splitBlankLines` in `api/internal/story/handlers_imports.go`). A Chinese .txt file that separates paragraphs with single newlines therefore imports each chapter as one paragraph. When no heading preset matches, the whole novel becomes one paragraph. A long translation can also exceed 20000 characters. Any later PATCH upsert of such a paragraph now fails validation with 400, so the user can no longer save edits to it. No data is lost, and the bound does its job against oversized writes. Suggested follow-up: split imported and model text on single newlines as well, or raise the text bound well above the largest paragraph the server itself produces. This does not block the merge.

### Low
2. **The reply token cap can be higher than a user-configured model allows.** `episodeOutputTokenCap` goes up to 16384 (`api/internal/story/output_token_cap.go`). Model names are free-form in LLM settings, so a model whose output limit is 8192 would get a 400 from its provider on a long target, where it used to get a truncation error. Either way the action fails, so this is only a change in the error message.
3. **An outline that runs out of insert retries may call the LLM again.** After `maxOutlineInsertAttempts` (5) collisions, `insertOutlinedEpisode` returns the unique violation, and the pipeline's normal retry reruns the whole step, LLM call included. This only happens when many concurrent generate runs target the same series, which is rare.
4. **The staging lock is per process on Windows** (`staging_lock_windows.go`). This is documented, and production pulls only run in Linux containers.
5. **A 403 caused by a bad signature is now retried until the transient attempts run out, instead of failing at once** (`transfer_status` in `streaming.py`). This is documented and bounded.

### Info
- `draft_step_applications.step_id` references `pipeline_steps(id)` with a single-column foreign key. Tenant ownership is enforced before the claim by `GetStepByID` with `tenant_id` and the scope/kind check in `ApplyDraftStep`. The `(tenant_id, draft_id)` foreign key to `episode_drafts` is composite. Table grants come from the default privileges in `20260925010000_db_roles.sql`.
- Apply-once concurrency is sound. The claim INSERT, the version-fenced update, the revision insert and the trim run in one transaction. A second concurrent claim blocks on the first one's uncommitted row. If the first rolls back after a version conflict, the second can still apply.
- In the pull outcome, a context cancel (owner pause, heartbeat loss or worker stop) counts as a pause. A deadline is transient and marks the row failed on the last attempt. The 3h `models.pull` override stays below the 4h `RescueStuckJobsAfter`, which `AssertTimeoutsBelowRescue` checks at startup.
- Security: the pieces checked were tenant scoping of the story queries (with `TrimDraftRevisions` now filtered by tenant), request-bound validation through `api/internal/validation/middleware.go`, the bible glossary rendered through React (no raw HTML), and the integration helper, which touches only `login:ip:%` buckets. I found no SSRF, injection, secret or authz problems. Roles on the changed routes are unchanged.

## Verification (run by the reviewer)
| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` (toolbox, `go test -race`) | EXIT 0: golangci-lint 0 issues, tenantctx and the tenant-query lint OK, models lint OK, ruff clean, 39 Go packages ok, pytest 72 passed and 2 skipped |
| Generated-code drift (`git status` after toolbox gen) | none |
| web `typecheck`, `lint`, `npm test`, `npx vite build`, `budget-check` (host) | all pass, 81 tests |
| Integration (`loomtale-d`, compose.yml + compose.integration.yml, heavy lock) | `ok loomtale/api/internal/integration`: 71 passed, 0 skipped, 0 failed. This includes `TestAcceptingAnInsertingStepTwiceInsertsItOnce`, `TestDraftPatchRejectsOversizedParagraphs`, `TestLoginRateLimitReturns429`, `TestLoginRejectsWrongPassword` and `TestDraftPatchVersionConflict`. The new migration was applied to a real Postgres. |
| Playwright e2e (`loomtale-d`, compose.yml, seeded owner, `--workers=1`) | 3 passed |
| Teardown | `down -v` done; no `loomtale-d` containers or volumes left. An e2e screenshot the run rewrote was reverted, not committed. |

## Success criteria
- The integration suite no longer hits 429: **met** (71/71 pass in one run, with no 429s).
- Each AI result is applied at most once, and draft writes are atomic: **met** (integration).
- The draft op bounds are enforced: **met** (integration).
- The Test endpoint reports latency, and the unit-level criteria for outline numbering, bible seed, importer and token cap are shown: **met** (unit tests with `-race`, web tests).
- A timed-out pull ends failed and can be resumed, and concurrent pulls download once: **met at unit level**. The live check is **pending: model downloads paused by the user**.
- An expired output URL is retried on a live GPU run: **pending: model downloads paused by the user** (met at unit level).
- The 45s client timeout on the provider probe: the test passed in 0.44s, which suggests that the sidecar's Claude CLI is not authenticated on this stack, so the test returned before its probe. The timeout is compiled and vetted, but the live probe path was not exercised. Doing that would need a logged-in Claude CLI, which is external.

## Unresolved questions
- Should the text bound on imported paragraphs be relaxed, or should the import split on single newlines, before real Chinese .txt files are imported (Medium 1)?
- Should the "Preface" chapter title be localised (this repeats the cook report's question)?
