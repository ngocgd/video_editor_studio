# Independent verification: characters, storyboard and scene editor (round 7, Docker back)

Branch `feat/characters-storyboard` @ `71ac60e` (worktree `.claude/worktrees/lane-a-7`), reviewed as `git diff main...HEAD` (149 files). `main` and `origin/main` are both `4962ecd`, and `git rev-list HEAD..main` is empty, so `main` is already merged into the branch. The code has not changed since `a9334ce`. The newer commits are review and cook-report documents only.

**Verdict: not ready to merge.** This is the first round with a working Docker engine. The toolbox, the integration suite (76 passed) and the live split on HEAD are green. The standard Playwright run fails, though. The phase 6 writer spec runs right after the new storyboard spec and gets HTTP 429 from the per-IP general API budget. It failed in 3 of 3 runs. This is the round 1 finding H2 ("the storyboard used up the per-IP API budget"). It was marked "fixed in code, verification pending", and this run shows it is not fixed.

## Checks run this round

| Check | Result |
|---|---|
| `scripts/tb.sh gen` | Exit 0. `git status --porcelain` is empty afterwards, so there is no generated-code drift |
| `scripts/tb.sh lint test` (with `-race`) | Exit 0. golangci-lint reported 0 issues and ruff "All checks passed". There were 42 `ok` Go packages with no FAIL and no DATA RACE. Pytest: 66 passed, 2 skipped |
| Web `typecheck`, `lint`, `npm test`, `npx vite build`, `budget-check` | All exit 0. Vitest ran 19 files and 91 tests. The budget check passed, with CSS at 6.94 KB |
| Integration (`loomtale-a`, heavy lock), first attempt | **Invalid.** `web` and `api` were not up (see note 1), so 66 tests skipped with "API not reachable" |
| Integration (`loomtale-a`, heavy lock), re-run | `up --wait --build` exit 0. **76 passed, 0 failed, 0 skipped** in 41.6 s. This includes `TestStoryboardSplitStepsStaleAndTakes`, `TestResplitAfterANarrationEditNeedsConfirmation`, `TestMediaVariantsAndPeaksOnLavfiFixtures` (with the Cache-Control assertion), `TestSceneRollupStaysFastAt400Scenes`, `TestVoiceCloneNeedsAuditedConsentAndScenesStayInTheirTenant`, `TestVoiceParamsCannotCarryServerControlKeys` and `TestCrossTenantAssetAccessReturns404` |
| Live split on HEAD (`COMPOSE_PROFILES=claude-cli`, `INTEGRATION_TAGS=integration,live`, `-run TestLiveLLMSceneSplit`) | **PASS** in 614.5 s. Seven claude-cli Continue rounds took the draft from 171 to 6,633 words. `llm.scene_split` step `01a0de26-79ad-7d13-8044-d746340e201f` (run `01a0de26-79ad-7d16-b12d-7ce772c34209`) took 6m55s and produced 82 scenes with 0 unrecognised speakers. Dialogue segments: Lin Mo 113, Elder Qiu 164 |
| Playwright, compose.yml only, default limits, `--workers=1` (3 runs on fresh or reused stacks) | **FAIL, 3 of 3.** models, smoke and storyboard-characters passed. `writer-import-settings.spec.ts` failed each time: once at line 75, where the writer stayed on "Loading…" after a reload, and twice at line 95, where the "Create EN draft" button never appeared |

The stacks were torn down with `down -v` after each stage, and no `loomtale-a` containers remain. The e2e runs rewrote the tracked screenshots under `reports/phase-06-screens` and `reports/phase-07-screens`. Those were restored with `git checkout`, so the working tree is clean.

## Findings

### Critical

None.

### High

- **H1. The standard e2e suite fails with 429s once the storyboard spec runs, because the per-IP general API budget is exhausted.**
  - **Evidence.** The Playwright trace of the full run shows the storyboard spec starting at 14:50:10 UTC. From 14:50:22 on, the writer spec gets 18 responses of 429. The affected calls include `GET /gpu`, `/readyz`, `/auth/me`, `/auth/csrf`, `/series/{id}/bible`, `/episodes/{id}/drafts/en` and `POST /episodes?seriesId=…`.
  - **Effect.** Because `POST /episodes` is refused, the spec navigates to `/episodes/undefined`, which returns 400, and the "Create EN draft" button never renders. The api log shows 88 accepted API requests in the ten seconds 14:50:10–14:50:19, which is the storyboard spec. That is close to the whole 100/min bucket (`API_RATE_LIMIT_PER_MINUTE`, default 100) before the writer spec starts. Rejected requests are not logged.
  - **What changed since round 1.** The storyboard fixes help but are not enough. Media redirects now have their own bucket, and the takes strip loads lazily after a 250 ms settle. The app shell still polls `/gpu`, `/readyz` and `/jobs`, and the storyboard, characters, voices and styles pages each refetch `auth/me`, `auth/csrf` and their lists. A single browser session of about 40 seconds therefore sends about 140 general-bucket requests.
  - **Why it matters beyond the tests.** One person working through the storyboard at a normal pace will hit 429s on the same budget that login draws from. The cook report's first workaround (raising the limit in the e2e script) was withdrawn in favour of making the default command pass, and the default command still fails.
  - **Fix directions (the fixer chooses one):**
    - Cut the request volume of the storyboard, characters and settings pages, and of the shell pollers.
    - Or size the general per-IP budget for a single-user SPA session, and prove it with the unchanged default Playwright command.
    - Or give each e2e spec an isolated budget without weakening production limits.
  - **Acceptance.** The default Playwright command passes 4 of 4 twice in a row on a fresh stack.

### Medium

- **M1 (open since round 1, re-confirmed on HEAD). The image step ignores inputs that its stale hash includes.** `scenes/inputs.go` `ImageComponents` hashes the style's `NegativePrompt` and `Sampler` and each character's `NegativePrompt`. `scenes/steps_image.go` sends none of them. So editing a negative prompt marks the images stale, but regenerating them cannot change the result. This does not block the merge.

### Low (not blocking, unchanged from round 6)

- L1–L4:
  - A confirmed split stores `discardWork` in the step input.
  - The media bucket is applied before the session middleware.
  - `GenerateMissing` has no double-submit guard.
  - `scenes.Changed` on a split lists only the kept ids.
  - There are races between take selection and `RecordTake`, and on the LoRA version.
  - Assets can be orphaned when takes cascade on a re-split.
  - There is no GIN index on `character_ids`.
  - `voiceparams.isFloat` accepts NaN and Inf.
- L5: `UpdateScene` accepts `segments` and `narration` together without checking that they agree.

## Independent review (re-checked this round)

- **Authorization.** All 32 new operations in `openapi/paths/{characters,scenes,presets,media}.yaml` declare `x-min-role`:
  - 8 GETs require viewer;
  - 11 POSTs, 6 PUTs, 2 PATCHes and 4 DELETEs require editor;
  - `backfillMedia` requires owner.
- **Tenant isolation.**
  - Every query in `db/queries/{scenes,takes,characters,presets,media}.sql` filters on `tenant_id`, including each LATERAL join in `SceneRollup`.
  - The new tables use composite `(tenant_id, id)` foreign keys to series, episodes, characters, voice presets and image styles.
  - Asset references (character refs, the LoRA dataset, preset reference audio) are loaded through the tenant-scoped `GetAssetByID` before they are stored.
  - The integration cross-tenant tests pass.
- **Injection and SSRF (ffmpeg).**
  - The argv is built from typed values, and each input has a forced demuxer.
  - Local inputs must be clean absolute paths strictly inside the step temp dir, and use the `file` protocol whitelist.
  - Concat is local only, with `-safe 1`.
  - Remote inputs must be https to the configured internal host, with no userinfo.
  - Muxers and codecs are allowlisted.
- **Asset variant redirect.**
  - The asset is loaded with the tenant scope, and only `ready` assets are served.
  - Variant keys come only from the stored `variants` JSON.
  - `Cache-Control: private, max-age` is half of the presign TTL.
  - The media bucket regex `^/api/v1/assets/[^/]+/variants/[^/]+$` matches the decoded path, so a login or other request cannot pass as a media redirect.
- **Data loss.** A re-split locks the episode's scenes `FOR UPDATE`. It refuses to drop edited scenes or takes unless `discardWork` is set, and the integration test for this passes.
- **Board.** No other lane has merged into `main` since this branch took it in. Lane a's phase 8 branch and lane f's 9c part 2 branch were built on this branch, so they inherit H1. Their e2e runs will hit the same budget unless H1 is fixed here first.

## Success criteria

| Criterion | Status |
|---|---|
| A 6k-word draft splits into scenes, with dialogue attributed to ≥2 characters that have voices assigned | **Met on HEAD (live)**: 6,633 words, 82 scenes, Lin Mo 113 and Elder Qiu 164 segments, 0 unrecognised speakers |
| Editing one scene's narration marks only that scene's voice, align and render pieces stale; regenerating queues exactly one GPU step | **Met** for voice and align (`TestStoryboardSplitStepsStaleAndTakes` passes). Render pieces arrive with phase 8 |
| The grid and timeline meet the 60fps budgets with 300+ scenes | **Met** at 450 scenes. Grid scroll averaged 16.67 ms/frame (60.0 fps), with p95 18.1–18.5 ms. One run under load averaged 56.8 fps with p95 18.1 ms. The timeline averaged 60.0 fps, and keyboard moves had a median of 1.6–4.4 ms. The storyboard spec's own assertions passed |
| Ollama variant | Deferred to phase 9c by design |
| The default e2e suite passes (H2 acceptance from the review fixes) | **Failed** (H1 above) |

## Notes

1. The first integration `up --wait --build` left `api` and `web` down. Its exit status is lost, because another lane's verify run redirected into the same shared scratchpad `heavy.log` (acknowledged on the board). The re-run `up --wait` exited 0, and all 76 tests passed. This does not look like a branch defect.
2. Workflow subagents share one scratchpad directory, and an `e2e.log` from 14:31 was briefly mistaken for this run's output. A board note asks lanes to prefix their scratch log names.

## Blocking items

1. Fix H1 so that `npx playwright test --workers=1` passes against `deploy/compose.yml` with the default rate limits. Then re-run Playwright under the heavy lock.
2. After that, the branch can merge into `main` under the merge lock. The toolbox, integration and live criteria are already green on `71ac60e`, but they must be re-run if the fix touches the api.

## Unresolved questions

- Should the general per-IP budget for a local single-user install stay at 100/min? A storyboard session alone uses most of it. Or is the intended fix to reduce the web client's request volume?
- Should the e2e specs write screenshots into the tracked `reports/phase-0*-screens` folders on every run? Each verification run dirties the working tree.
