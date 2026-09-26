# Independent review, round 1: character LoRA training, scene scoring and depth, LLM default seed

- Branch `feat/lora-character-wiring`, worktree `.claude/worktrees/lane-f-9c2`. Reviewed head `e586386` plus the merge of main `c05667d` made by this verifier as `9140632` (one conflict, `api/internal/httpapi/gen/server.gen.go`, resolved by regenerating with `scripts/tb.sh gen`; a second `tb.sh gen` run leaves no drift).
- Scope: character LoRA training on the pyworker trainer, the `image.score` and `image.depth` steps, the seed-LLM-default migration. Weight-dependent criteria and the model sign-off stay pending (model downloads paused by the user).
- Reviewer: lane b verifier. No code was changed; only this report is committed.

## Verdict

PASS. No Critical or High findings. Two Medium findings (M1 dataset memory and size cap, M2 the seed only fires if the LLM is installed before the migration runs) and five Low findings are recorded below; none blocks the merge. Every check is green and every in-scope success criterion is either met by built and tested code or pending because model downloads are paused by the user.

## What was reviewed

`git diff main...HEAD` without generated code: `api/internal/characters/{steps.go,http.go}`, `api/internal/providers/train/dataset.go`, `api/internal/scenes/{steps.go,steps_image.go,steps_image_analysis.go,steps_score_depth.go}`, `api/cmd/worker/main.go`, `db/queries/takes.sql`, `db/migrations/20260927600000_seed_llm_default.sql`, `openapi/schemas/characters.yaml`, `api/internal/bench/train_suite.go`, the new unit and integration tests, the one web text change, and the pyworker contract they call (`engines/train_config.py`, `engines/train_dataset.py`, `servicers/train_service.py`, `engines/dinov2_score.py`, all already on main).

Checked and found sound:

- Tenant isolation. Every new query path is tenant-scoped: `GetAssetByID`, `GetTake`, `GetSelectedTake`, `ListCharacterRefsBySeries` and the new `MergeTakeParams` all filter on `tenant_id`, and the analysed take must be an image take of the step's own scene. The dataset of a training run is read with the run's tenant, and the explicit dataset of the API call is checked against the caller's tenant before it is stored.
- Secrets and injection. No credentials are added; captions and trigger words go into a zip entry and a validated trigger word (`[a-z][a-z0-9_]{1,31}` on both sides), never into a shell or SQL string. The LoRA file name is built from the character UUID and an integer version, so the write under `ModelsDir/loras` cannot escape the folder, and `FGetObject` writes through a temporary `.part.minio` file and renames it, so ComfyUI never reads a half-written LoRA.
- SSRF. The only URLs handed to the Python worker are internal presigned GET/PUT URLs built by the worker itself; nothing user-supplied becomes a URL.
- Authorization. No new API operation; `TrainCharacterLora` keeps its existing `x-min-role`. The API now rejects datasets outside 4 to 64 images and non PNG/JPEG/WebP images up front, matching the trainer's own bounds (`MIN_IMAGES`, `MAX_IMAGES`, suffix set) and the step re-checks the bounds.
- Concurrency and data loss. `MergeTakeParams` merges with `params || patch` in one statement, so the score and depth steps cannot overwrite each other's keys or the image step's keys; a deleted take makes the step fail with a validation error instead of writing to nothing. The trainer params are whitelisted (`steps`, `rank`, `learning_rate`, `max_minutes`), and the trigger word and output key cannot be overridden from stored params. The step and API bounds (steps 100 to 4000, rank 4 to 64) sit inside the worker's accepted ranges (10 to 4000, 4 to 64).
- Residency. The train, score and depth steps all declare a pyworker `ModelRef`, so they take the single GPU slot under the residency manager, and the analysis run is only queued when the install check passes for the engine, so a machine without the vision weights never queues steps that can only fail.
- Migration. `20260927600000_seed_llm_default.sql` is inside lane f's range, above every migration on main, idempotent (`NOT EXISTS` plus `ON CONFLICT DO NOTHING`), only touches tenants without an `llm_settings` row, and its Down only deletes rows still unchanged since the seed. The unit test keeps its candidate list equal to the manifest's Ollama entries.
- Pact with the render pipeline: the take params keys (`score`, `scoreMin`, `scoreMax`, `scoreReferences`, `scoreEngine`, `depthAssetId`, `depthEngine`) are as announced on the board; no render-pipeline file is edited.
- Commit hygiene: conventional commits, no AI references, no plan or finding IDs in code, comments, migrations, test names or commit messages.

## Findings

### Critical

None.

### High

None.

### Medium

**M1. A large training dataset can exhaust the shared worker's memory and always fails at the trainer above 512 MiB.** `TrainHandler.Run` (`api/internal/characters/steps.go`) reads every dataset image fully into memory (`ReadAll`, up to 25 MiB each by `storage.MaxBytesByKind["image"]`) and then builds the whole zip in a second in-memory buffer (`train.DatasetZip`). With the allowed 64 images that is up to 1.6 GiB of images plus up to about 1.6 GiB of archive, more than the worker's `mem_limit: 3g` (`deploy/compose.yml`), and the worker runs every tenant's steps. Separately, the Python worker refuses any dataset archive over `MAX_DATASET_BYTES = 512 MiB` (`servicers/train_service.py`), so any dataset over 512 MiB is read, zipped and uploaded only to fail. Scenario: an editor approves 64 near-25 MiB PNG references and starts training; the worker is OOM-killed (and again on each retry), interrupting other tenants' steps. Suggested fix: cap the total dataset bytes at the trainer's 512 MiB in the API (sum of asset `bytes`) and the step, and stream the archive to a temporary file (or straight to the PUT) instead of holding both copies. Not blocking: it needs deliberate, unusually large references by an authenticated editor, and typical references are 1 to 3 MiB.

**M2. The LLM default seed runs once, at migration time, so on a deployment that installs its local LLM later it never seeds.** Goose applies `20260927600000` once and records it; the seed only happens if `model_installs` already shows `qwen3.5-9b` or `gemma-4-12b` installed at that moment. Any deployment that migrates before pulling an Ollama model (every fresh install, and the user's stack today, since model downloads are paused) records the version as applied with no rows seeded, and installing the model afterwards changes nothing. The cook report and the commit subject ("seed Ollama as the default LLM once a local LLM is installed", "it takes effect only once a signed-off Ollama model is installed") describe behaviour goose does not provide. The migration is harmless and correct as a no-op, but the success criterion "Ollama is the seeded default LLM" will need further work after the sign-off (a new seed migration at that time, or a seed triggered by the model install), not only the model download. Recorded so the sign-off step plans for it.

### Low

- **L1.** The score and depth `InputHash` hash the scene's selected image take while `Run` analyses the take named in the step input. When a new take is not the selected one, the stored hash describes a different take. Nothing reads staleness for these steps today (`pipeline.Stale` has no production caller), so there is no behaviour impact yet; hashing the input take when present would keep it right.
- **L2.** The caption is the trigger word plus the character's appearance prompt; the prompt allows 2,000 characters but the trainer refuses captions over 4 KiB (`MAX_CAPTION_BYTES`). A prompt written in CJK over about 1,360 characters would fail training with a validation error. Truncating the caption at a rune boundary would avoid it.
- **L3.** An explicit `datasetAssetIds` list may name any ready image of the tenant, not only the character's approved references, while the security checklist says "training dataset limited to approved refs of the tenant". Tenant isolation holds; this predates the branch and is a product choice to confirm.
- **L4.** Each depth run stores a new depth asset and the take keeps only the latest id; earlier depth maps of the same take are not removed. Storage growth only.
- **L5.** Migration ordering (the open question from the characters and storyboard review, M2 there): this branch adds `20260927600000`; branches still to merge with lower versions (render pipeline `20260927100000`, analytics `20260927500000`) will hit goose's "missing migrations" refusal on any database that already applied this one. Awaiting the lead's decision; not introduced by this branch.

## Verification run by the reviewer

All on the branch after merging main (`9140632`), Docker 29.2.1:

- `scripts/tb.sh gen`: regenerated the conflicted `server.gen.go`; a second `tb.sh gen` run followed by host `git diff --exit-code` shows no drift.
- `scripts/tb.sh lint test`: golangci-lint 0 issues, tenantctx and the tenant-query lint clean, `go test ./... -race -count=1` in `api` and `tools` all packages ok, `uv run pytest -q` 108 passed, 5 skipped.
- Web (host node 22): `npm run typecheck`, `npm run lint`, `npm test` (21 files, 107 tests), `npx vite build` and `npm run budget-check` all pass.
- Integration under the heavy lock (`b-9c2`, compose project `loomtale-b`, compose.yml + compose.integration.yml): 84 passed, 0 failed, 0 skipped, including `TestSeedLLMDefault`. The stack started first time.
- Playwright under the same lock (compose.yml only, owner seeded): 5 passed of 5 (models, smoke, storyboard and characters at 450 scenes, writer/import/settings, YouTube settings).
- Teardown: `down -v` for both stacks; no `loomtale-b` containers or volumes remain. The screenshots the e2e run rewrites under the plan's report folders were restored, not committed.

## Success criteria (in scope)

- Two character LoRAs exist and are used: pending, model downloads paused by the user (and the trainer packaging, the `train` extra, awaits the user's decision). The wiring is built and unit-tested; the scene image step already passes the character's LoRA file to the Z-Image workflow's `lora_name`.
- Scenes carry consistency scores: pending, model downloads paused by the user (DINOv2 weights). Step code, queueing and param merge are built and unit-tested.
- Parallax is enabled: pending, model downloads paused by the user (depth weights). The `/gpu` `depth` capability is not in this run's scope and stays a follow-up.
- The benchmark report exists and the user signed off the default models: pending, model downloads paused by the user.
- Ollama is the seeded default LLM: pending, model downloads paused by the user and the sign-off; see M2 for the extra work the seed will need.
- `/gpu` shows one resident model at a time across the cycles: pending, model downloads paused by the user.
- Idempotent seeding test (`TestSeedLLMDefault`): met, in the 84/84 integration run above (Down/Up/Up/Down replay in a rolled-back transaction).

## Unresolved questions

- Lead: migration ordering (L5) — renumber later-merging migrations above main's maximum, or enable goose's allow-missing mode?
- User: should the LLM default seed be re-applied at sign-off by a new migration, or triggered when an Ollama model finishes installing (M2)?
- User: approve the ai-toolkit `train` extra packaging so the trainer can run at all?
