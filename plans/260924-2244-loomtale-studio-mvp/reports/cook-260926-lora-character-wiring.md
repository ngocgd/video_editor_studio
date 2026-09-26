# Phase 9c part 2 — character LoRA training, scene scoring/depth and the LLM default seed

Branch `feat/lora-character-wiring` (worktree `.claude/worktrees/lane-f-9c2`), cut from `feat/characters-storyboard` with `feat/lora-scoring-engines` (phase 9c part 1) merged in as 90b4758. Not merged into main and not pushed. This was a code-only run: Docker was down for the whole run and model downloads are paused by the user.

## What shipped

- 633dc5c `refactor(train)`: `api/internal/providers/train/dataset.go` holds the shared trainer dataset archive (`DatasetZip`) and trigger word rules (`TriggerWord`); the bench train suite now uses it (`.jpeg` entries are written as `.jpg`).
- b211627 `feat(characters)`: the character LoRA training step runs on the pyworker `z-image-turbo-trainer` engine (base model `z-image-turbo`), reads only the tenant's approved refs, writes the weights under `ModelsDir/loras` and records a `character_loras` version. `api/cmd/worker` passes `ModelsDir`. The `LoraTrainRequest` API bounds were tightened to what the trainer supports: 4–64 dataset assets, at most 4000 steps, rank at most 64 (OpenAPI bundle, Go server and web client regenerated).
- 41a6449 `feat(scenes)`: `api/internal/scenes/steps_score_depth.go` adds `image.score` (DINOv2 cosine similarity against the scene characters' approved refs, merged into the image take params as `score`, `scoreMin`, `scoreMax`, `scoreReferences`, `scoreEngine`) and `image.depth` (Depth-Anything-V2-Small depth map stored as an `image/png` asset, `depthAssetId` and `depthEngine` in the take params). `steps_image_analysis.go` queues a `scene.analyze` run after a new image take, but only when the model install check passes for the engine, so nothing is queued on a machine without the weights. References are picked round-robin per character, at most 32. New query `MergeTakeParams` in `db/queries/takes.sql`. Analysis never creates a take, bumps the scene version or fires the change hook, so it does not supersede renders. Unit tests in `steps_score_depth_test.go`.
- 28e93b5 `feat(worker)`: the worker gives scene steps the vision client and the install check.
- 4bffa65 `feat(db)`: migration `20260927600000_seed_llm_default.sql`. It sets `llm_settings.default_provider = 'ollama'` only for tenants that have no `llm_settings` row, and only when `model_installs` shows `qwen3.5-9b` or `gemma-4-12b` installed, so it is a no-op on main today. Seeded tenants are recorded in `llm_default_seeds`; Down removes only seeded rows still unchanged since seeding. Up is re-runnable. A unit test keeps the migration's candidate list equal to the manifest's Ollama LLM entries; the integration test `TestSeedLLMDefault` replays Down/Up/Up/Down inside a rolled-back transaction.

## Verification

Host toolchain (Go 1.26.8, golangci-lint 2.14.0, uv, node 22), run at the branch head:

- Generated code: bundle, oapi-codegen, sqlc, model asset sync and `npm run gen` re-run; `git diff --ignore-cr-at-eol --exit-code` shows no drift.
- `go vet ./...` and `golangci-lint run` in `api` and `tools`, with and without the `integration` build tag: 0 issues.
- `tenantctx ./...` in `api`: clean. `scripts/lint-tenant-queries.sh`: OK.
- `go test ./... -count=1` in `api` and `tools`: all packages pass (no `-race`, the host has no C compiler).
- `uv run pytest -q` in `workers-python`: 102 passed, 5 skipped.
- Web: `npm run typecheck`, `npm run lint`, `npm test` (19 files, 91 tests), `npx vite build` and `npm run budget-check`: all pass.
- Pending: Docker down — `scripts/tb.sh gen-check lint test`, the integration suite (including `TestSeedLLMDefault`) and Playwright e2e under the heavy lock. These must run once the user restarts Docker.

## Success criteria

- Two character LoRAs exist and are used: pending, model downloads paused by the user (the training wiring is built and unit-tested; the trainer packaging, the `train` extra, also still awaits the user's approval, and without it the engine answers `engine_not_installed`).
- Scenes carry consistency scores: code built; the live run is pending, model downloads paused by the user.
- Parallax is enabled: pending, deferred to the second part of this phase (needs the worker heartbeat to report capabilities so `/gpu` can show `depth`), and the depth weights are pending because model downloads are paused by the user. Phase 8 parallax stays disabled meanwhile, which is safe.
- The benchmark report exists and the user has signed off the default models: pending, model downloads paused by the user.
- Ollama is the seeded default LLM: the migration is built; it takes effect only once a signed-off Ollama model is installed, so the seeding itself is pending, model downloads paused by the user. Per-action overrides from the sign-off are pending for the same reason.
- `/gpu` shows one resident model at a time across the cycles: pending, model downloads paused by the user.

## Deviations

- The migration writes only the provider name. Which Ollama model answers is deployment configuration (`LOOMTALE_OLLAMA_MODEL`), so the seed does not pin a model name.
- Tenants created after the migration fall back to the configured default rather than a seed row.
- Score with no approved references succeeds with the output marked skipped and no score written.
- `/gpu` `capabilities: depth` was not added: the worker heartbeat (`providers/workerstatus`) has no capabilities field, and adding one changes the status schema owned by phase 3.

## Follow-ups

- Add a capabilities field to the worker heartbeat and report `depth` on `/gpu` once Depth-Anything-V2-Small is installed.
- When downloads resume: pull the trainer, DINOv2 and depth weights, train two characters, score an episode, run the benchmark suites, get the default-model sign-off and confirm the seed.
- Run the full toolbox, integration and e2e verification once Docker is back.

## Unresolved questions

- Does the user approve the ai-toolkit `train` extra packaging (lane b)?
- Which per-action LLM overrides, if any, should the seed apply after the sign-off?
