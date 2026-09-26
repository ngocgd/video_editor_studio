# Independent verification: characters, storyboard and scene editor (round 1)

Branch `feat/characters-storyboard` @ `805e9b5` (worktree `.claude/worktrees/lane-a-7`), reviewed as `git diff main...HEAD` against the merge base `367c0d7`. Heavy runs used the compose project `loomtale-a` under the shared heavy lock, and every stack was torn down with `down -v`.

**Verdict: not ready to merge.** Two High findings are open, and the project's standard Playwright run fails with the default rate limit. Every other check is green. Three of the four success criteria were observed; the Ollama variant is deferred by design.

## Checks

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` | exit 0. golangci-lint reports 0 issues. The tenant-query lint and the manifest lint pass. 40 Go packages pass with `-race`, and pytest passes 26 tests |
| Drift of generated code (host `git status` / `git diff --exit-code` after `gen`) | Clean |
| Web: typecheck, lint, vitest, `vite build`, `budget-check` | All exit 0. vitest passes 18 files and 88 tests. The storyboard route chunk is 15.89 KB gzip (limit 120 KB). The authenticated shell is 166.85 KB, which passes the 200 KB cap but exceeds the 160 KB target |
| Integration suite (compose.yml + integration override) | **72 passed, 0 failed, 0 skipped** (43.6 s) |
| Playwright `--workers=1`, compose.yml only, default `API_RATE_LIMIT_PER_MINUTE` (100) | **Failed: 3 passed, 1 failed.** `writer-import-settings.spec.ts` timed out waiting for the post-login redirect. It ran right after the new storyboard spec on the same per-IP budget (see H2) |
| The same run with `API_RATE_LIMIT_PER_MINUTE=1000` | 4 passed |
| Live LLM split (`COMPOSE_PROFILES=claude-cli`, `INTEGRATION_TAGS=integration,live`) | Passed (706.8 s). Evidence is under the success criteria below |
| Trial merge into current `main` (`git merge-tree`) | Conflicts in `api/cmd/worker/residency.go`, `api/internal/db/gen/models.go` and `api/internal/db/gen/querier.go` (see M1) |

Stack start-up was flaky on this host. `minio-init` failed with "connection refused" to `minio:9000` on 4 of 8 `up --wait` attempts, and a teardown followed by a single retry always recovered. This phase does not touch MinIO, so the flake is noted as an open question rather than a finding.

## Findings

### High

**H1. Voice parameters can set `reference_url` and `consent`, which bypasses the audited cloning consent and lets the Python worker fetch any URL.**
- **Where:**
  - `api/internal/scenes/inputs.go:71` (`MergedParams`)
  - `api/internal/scenes/steps_voice.go:132-147`
  - `api/internal/characters/steps.go:351-363`
  - `openapi/schemas/presets.yaml` `StringParams` (any key is accepted)
- **Why:** The TTS params sent to the pyworker start from the preset and voice params, which the user controls. Only `language` and `output_key` are then overwritten. `reference_url` and `consent` are set by the server only when the preset has a reference asset. Otherwise, the values the user supplied pass through unchanged.
- **Scenario:** An editor sends `POST /settings/voice-presets` with `{"name":"x","engine":"chatterbox","params":{"reference_url":"http://<any host reachable from gpu_net>/clip.wav","consent":"granted"}}`.
  - The request returns 201. No consent check runs and no `voice_reference_consented` audit row is written.
  - The editor then assigns the preset to a character or the narrator.
  - `voice.synthesize` and `voice.preview` send that URL with `consent=granted`.
  - On main, `tts_service.py` accepts the URL and downloads it (`fetch_input` has no host check), then clones the voice.
- **Impact:** This defeats the security checklist item "Voice cloning refs: consent … recorded in audit log". It also gives an editor an SSRF primitive from the GPU network.
- **Fix:**
  1. Reject reserved keys (`reference_url`, `consent`, `output_key`, `language`) on every write of `StringParams` with a 422. Preferably, allow only per-engine tuning keys such as `exaggeration`, `cfg_weight`, `temperature` and `seed`.
  2. Also strip those keys in `MergedParams` before the server sets its own values.
  3. Add a unit test and an integration test for the refusal.

**H2. The storyboard sends one rate-limited API request per selection move and per image tile. The default per-IP budget runs out, and the standard e2e run fails.**
- **Where:**
  - `web/src/features/storyboard/scene-inspector.tsx:38`: `useTakes` refetches `listSceneTakes` on every active-scene change.
  - `web/src/features/storyboard/use-scenes.ts:17`: every tile image and every audio play is `/api/v1/assets/{id}/variants/...`, a 302 without cache headers.
  - `api/cmd/api/main.go:315-326`: the general limiter (100 requests per minute per IP) covers all of `/api/v1`.
- **Scenario:**
  - Holding J/K or an arrow key walks the grid at key-repeat speed, which is one takes request per scene.
  - Scrolling a storyboard with real images loads one variant redirect per mounted tile and again on every remount.
  - At about 100 requests a minute from one IP (one user, or a whole office behind NAT), every call returns 429: tiles, audio, SSE resync and even login. Images and audio appear broken with no retry.
- **Observed:** With the default limit, the Playwright run failed. The writer spec's login never redirected after the storyboard spec ran on the same IP. With a limit of 1000 the same run passed. The cook report deviation 7 records the same symptom and raised the limit for e2e only.
- **Why it matters now:** The e2e seed has no images yet. Once phase 9a produces images, a 300–450 scene storyboard cannot be browsed under the default configuration.
- **Fix (one or more):**
  - Debounce or cancel the takes fetch while the selection is moving, or return take summaries with the list.
  - Serve variant and audio redirects outside the general bucket, for example as a separate, larger per-session bucket. Give the 302 `Cache-Control: private, max-age` below the presign TTL.
  - Or raise the default limit on purpose and document why.
- **Acceptance:** The standard e2e command passes with the default limit.

### Medium

**M1. The branch is 20 commits behind `main`, including the merged TTS, align and local LLM phase. The merge conflicts, and one contract has drifted.**
- The trial merge conflicts in `api/cmd/worker/residency.go`, where main added `OllamaPreparer` and changed the `pyworker.Backend` fields, and in generated `db/gen` files. Regenerate these rather than hand-merging them.
- After the merge, `steps_voice.go:151` sends `Voice: <preset UUID>`. main's `vieneu.py:103` treats `job.voice` as a built-in voice name, so a Vietnamese preset without a reference clip will fail. main's `chatterbox.py:55` needs a reference clip on every request, so a `chatterbox` voice without a cloned preset will fail with a clear error.
- **Fix at merge:**
  1. Resolve `residency.go`, keeping main's backend wiring plus this branch's `gpuClients` return.
  2. Run `tb.sh gen`.
  3. Send a built-in voice name (a preset param or field), not the preset id, or send nothing.
  4. Rerun the full verification on the merged tree.

**M2. "Split by paragraphs" and "Re-split with AI" delete every scene whose narration changed, together with all its takes, in one click and without a confirmation.**
- **Where:** `web/src/features/storyboard/storyboard-view.tsx:233` and `api/internal/scenes/service.go` (`ApplySplit` → `DeleteScenesExcept`).
- **Scenario:** A storyboard was split by the LLM, and its images and voices were generated (hours of GPU time). A click on "Split by paragraphs" regroups the paragraphs, so almost no `text_hash` matches, and every take is deleted. There is no undo.
- **Fix:** When scenes with takes would be dropped, show a confirmation that states how many takes will be lost. Optionally add a dry-run count from the API.

**M3. The image step ignores inputs that its stale hash includes.**
- **Where:** `api/internal/scenes/steps_image.go`.
- **Problem:**
  - `ImageComponents` hashes the style's negative prompt and sampler, and each character's negative prompt, but `Run` never sends them.
  - Character reference images are never used, although the phase names "style + character LoRA/refs → ComfyUI workflow".
- **Impact:** Editing a negative prompt marks images stale, but regenerating them cannot change the result.
- **Fix:** Pass `negative_prompt` and the sampler when the template declares them. Otherwise, drop them from the hash and record ref usage as a phase 9a follow-up.

### Low

- **L1.** `TrainCharacterLora` (`characters/http.go:424`) runs `NextCharacterLoraVersion`, the insert and `Enqueue` outside one transaction.
  - Two concurrent requests collide on `(character_id, version)` and return a 500.
  - If the enqueue fails, a `queued` LoRA row is left that never trains.
- **L2.** `selectTake` can race a concurrent `RecordTake` on the same scene and kind. The partial unique index `scene_takes_one_selected_idx` then fails the GPU step after its work is done, and the retry reruns the synthesis. Taking `SELECT … FOR UPDATE` on the scene row first would serialise them.
- **L3.** Assets and objects are orphaned in two cases:
  - Takes dropped by a re-split leave their `assets` rows and MinIO objects behind.
  - Per-segment voice objects under `derived/` are never deleted. The cook report already lists this.
- **L4.** `CharacterEpisodeAppearances` joins every scene of the tenant on `ch.id = ANY(s.character_ids)` without a GIN index. The Characters page cost grows with the tenant's total scene count.
- **L5.** The phase file's "Security checklist" boxes are still unchecked, although the cook report claims those items.

## Verified as correct

- **Tenant isolation:**
  - Every new table carries `tenant_id`, and child foreign keys use `(tenant_id, id)`.
  - Every new query filters by tenant, and the lint covers the new tables.
  - Scene character and speaker ids are checked against the series. Asset, preset and style ids are looked up per tenant.
  - Integration confirmed that another tenant gets 404 for scenes, characters, voices and variants.
- **Authorisation:** `x-min-role` is set on all 32 new operations. Reads are viewer, writes are editor, and backfill is owner.
- **FFmpeg runner:**
  - Argv is built from typed values only. `-hide_banner -nostdin -loglevel error` is always set.
  - Both protocol whitelists live in one file.
  - Input formats are forced with `-f`, and concat runs with `-safe 1` and plain names.
  - Temp-dir path checks and the https internal-host check work, and stderr is capped and scrubbed. The unit tests cover the refusals.
- **LLM split:**
  - The model sees only labels and a roster of names.
  - Names are case-folded and resolved against the series' characters only. A forged UUID is flagged as an unrecognised speaker.
  - Draft text travels in a nonce-fenced data block with taint.
- **Takes and staleness:**
  - Stale hashes are built from named components, and a narration edit leaves the image take valid.
  - `scenes.Changed` fires once per mutation.
  - Regenerate queues exactly one gpu step at priority 2. Generate missing runs at priority 3 with three residency switches for 48 scenes.

## Success criteria

| Criterion | Status and evidence |
|---|---|
| A 6k-word draft splits into scenes, with dialogue attributed to ≥2 voiced characters | **Met (live, claude-cli).** Seven Continue steps took the draft from 171 to 6,515 words. The first steps were `01a0dca5-214f-795c-8109-36fea4624580`, `01a0dca5-96db-7413-939f-2f90f9b3afa6` and `01a0dca6-0c54-7a94-a2d5-ff440a6c357c`, and the last were `01a0dca7-499f-795a-9e31-3f0f65346a86` and `01a0dca7-b365-73b8-b893-ef5999acafce`. `llm.scene_split` step `01a0dca8-1d28-7870-b954-37e9c567c695` (run `01a0dca8-1d28-7872-840e-06498b4a2f99`), provider claude-cli, took 8m31s and produced **72 scenes** with 0 unrecognised speakers. Dialogue segments: Lin Mo 150, Elder Qiu 205, both with EN voices assigned |
| Editing one scene's narration marks only that scene's voice, align and render pieces stale; regenerating queues exactly one GPU step | **Met for voice and align** (`TestStoryboardSplitStepsStaleAndTakes` passed). The image stays done, other scenes are untouched, and the stale filter returns exactly that scene. Regenerate queued one `gpu` step at priority 2. Render pieces arrive in phase 8, which subscribes to `scenes.Changed` |
| Grid and timeline at 60fps with 300+ scenes of seeded data | **Met (quick check)** at 450 scenes (2:45:41 of timeline): grid 60.0–60.1 fps (p95 18.5–18.6 ms) with at most 14 mounted tiles; keyboard move median 5.2–5.5 ms (max 13.1 ms); timeline 60.0–60.2 fps. The seed has no images or waveforms. The phase 12 trace remains |
| Ollama variant | **Deferred to phase 9c** by design |

## Blocking items for the next round

1. **Fix H1.** Refuse and strip the reserved TTS param keys. Add tests showing that a preset or voice with a `reference_url` or `consent` param is refused.
2. **Fix H2.** Make the standard Playwright command (compose.yml only, default limit, `--workers=1`) pass without raising `API_RATE_LIMIT_PER_MINUTE`.
3. **Before merging (M1),** merge `main` into the branch, regenerate code, fix the TTS `voice` contract, and rerun the full verification on the merged tree.

## Unresolved questions

- Is the `minio-init` start-up race (4 of 8 `up` attempts here) known on `main`? It looks like the bitnami MinIO restart racing its healthcheck.
- Should `pipeline_steps_scope_latest_idx` stay in this phase's migration, since phase 3 owns the pipeline tables? The rollup budget needs it (median 5.9 ms at 400 scenes).
- Is the 166.85 KB authenticated shell (target 160 KB) acceptable until the generated client is split per domain?
