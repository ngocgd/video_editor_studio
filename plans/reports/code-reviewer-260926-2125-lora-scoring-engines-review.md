# Review: LoRA trainer, consistency scoring and depth engines (independent verification, round 1)

- Branch: `feat/lora-scoring-engines` at `2cbaac9`. Its base is `4962ecd`, which is the current `main`, so there was nothing to merge before verifying. Worktree: `.claude/worktrees/lane-b-9c1`.
- Scope: `git diff main...HEAD` covers 30 files and about 3.0k added lines. It adds three pyworker engines (`dinov2_score`, `depth_small`, `aitoolkit_train`) with their helpers, real `Score`/`Depth`/`Train` servicers, the `vision` extra, three manifest entries (`dinov2-base`, `depth-anything-v2-small`, `z-image-turbo-trainer`), the `vision`/`train`/`train-smoke` bench suites with a `--refs` flag, and tests.
- This is a partial phase. The second part covers the `image.score`/`image.depth` steps, the character LoRA wiring, `/gpu` `depth`, the seed-LLM-default migration, the benchmark report and the sign-off. That part is on `feat/lora-character-wiring` and is out of scope for this review.
- Verdict: **PASS.** There are no Critical or High findings, and every check is green. Every success criterion in scope is either met or pending for an external reason: the user paused model downloads, the work is deferred to the second part of the phase, or the train packaging is waiting for a user decision.

## Verification (re-run independently)

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` | Exit 0. golangci-lint reported 0 issues, and tenantctx and `lint-tenant-queries` are OK. The manifest lint passed (12 models, 4 workflows, 2 Modelfiles). All Go packages passed with `-race`, including `internal/bench` and `internal/models`. pytest: 102 passed and 5 skipped (the skipped tests are `-m gpu` and need weights) |
| Generated-code drift (host `git status`/`git diff` after `gen`) | Clean. The branch touches no openapi, proto, sql or migration files. `models/manifest.yaml` is byte-identical to `api/internal/models/assets/manifest.yaml` |
| Web: `typecheck`, `lint`, `npm test`, `npx vite build`, `budget-check` | All passed: 77 tests in 16 files, and the bundle budget check passed. The branch has no web changes |
| Integration (`loomtale-b`, heavy lock) | Exit 0: 75 `--- PASS` lines (including subtests), with no FAIL and no SKIP. `TestLoginRateLimitReturns429` and `TestModelFileDigestsAcceptSha256AndGitBlobPins` passed. The stack was brought down with `down -v` |
| Playwright e2e (`--workers=1`, heavy lock) | 3 passed. The owner was seeded with `migrate create-owner`, and the stack was brought down with `down -v`. No `loomtale-b` containers remain |
| Board | No CHANGE line from another lane touches this branch's files. Lane f (`feat/lora-character-wiring`) builds on this branch and consumes the engine names and the `Train`/`Score`/`Depth` params, which are unchanged here |
| Commit hygiene | 7 conventional commits with no AI references and no plan or finding IDs in code, comments or tests. No secrets or `.env` are committed |

No model weights, engine extras or images with extras were downloaded.

## Findings

### Critical / High
None.

### Medium (not blocking)
1. **The trainer can never run in any image yet** (`deploy/docker/pyworker.Dockerfile`, `workers-python/pyproject.toml`, `engines/aitoolkit_train.py`). The phase lists the Dockerfile extras `train` and `vision`, but only `vision` was added. No `train` extra and no ai-toolkit checkout at `AI_TOOLKIT_DIR` ship. `z-image-turbo-trainer` therefore always answers `engine_not_installed`. That answer is honest and safe on `main`, but "LoRA training" is not deliverable until the packaging lands. The implementer's handoff records why: ai-toolkit@60d0c28 needs `transformers==5.5.3`, `huggingface_hub==1.23.0` and a diffusers git commit. Those pins clash with chatterbox-tts (diffusers 0.29) and with the align/vision `transformers==5.2.0`, so the trainer needs a separate conflicting extra or a second pyworker image. Adding third-party code from a GitHub archive needs the user's approval. This is recorded as pending on a user decision, not as a defect.

### Low
2. **An engine name that does not match the RPC fails as INTERNAL** (`servicers/vision_service.py`, `servicers/train_service.py`). If `Score` names `depth-anything-v2-small`, the depth engine returns a `DepthOutput`, and `out.score` then raises `AttributeError`. If `Depth` names `dinov2-base`, the engine fails on `job.reference_paths`. Both surface as INTERNAL, which may be retried, rather than INVALID_ARGUMENT. The TTS and align servicers on `main` use the same pattern, and the only callers are internal (the bench and part 2 steps, which pass constants). Suggested fix: check `engine.task` against the RPC in one shared place.
3. **A corrupt dataset archive entry surfaces as INTERNAL** (`engines/train_dataset.py`). A bad CRC, or a broken deflate stream while `zf.open(info).read(...)` runs, raises `zipfile.BadZipFile` or `zlib.error`. Only `InvalidJobError` is mapped to INVALID_ARGUMENT, so these reach the client as INTERNAL. Suggested fix: wrap the extraction loop and re-raise as `InvalidJobError`.
4. **The trainer's process group is only killed on cancellation or timeout** (`engines/aitoolkit_train.py` `_kill`). When `run.py` exits on its own, or has already exited when `_kill` runs, `killpg` is skipped. A leftover grandchild, such as a dataloader worker, could then keep VRAM after the job. Suggested fix: always `killpg` the session after the leader exits, ignoring `ProcessLookupError`.
5. **The relay buffer has no bound** (`_relay`). `pending` grows without limit if the child writes a very long run with no `\n` or `\r`. Suggested fix: cap it, for example by dropping anything beyond 64 KiB.
6. **The operator docs do not mention `vision`** (`.env.example` `PYWORKER_EXTRAS` comment, `deploy/compose.gpu.yml` pyworker comment). Both still show `"tts-en tts-vi align"` as the GPU deployment set. The Dockerfile comment was updated, but an operator who follows `.env.example` builds an image in which DINOv2 and depth answer `engine_not_installed`.
7. **train-smoke with exactly four refs scores a training image against itself** (`api/internal/bench/train_suite.go` `RunTrainSmoke`). The held-out image falls back to `refs[0]`, which is part of the scored set, so the score is inflated. The doc comment and the script usage say this, so it is a limitation of the benchmark, not a bug.

### Informational
- This review re-ran pip-audit (`uv export --frozen --extra vision` piped to `pip-audit --no-deps --disable-pip`, so nothing was installed). It found 5 advisories, all in transformers 5.2.0: PYSEC-2026-2289 (fixed in 5.3.0), PYSEC-2026-2290 (fixed in 5.5.0) and PYSEC-2026-3929 (fixed in 5.10.0). torch 2.9.1+cu128 cannot be audited from PyPI, and the implementer's handoff lists further torch advisories. The align extra on `main` already ships the same pins. The vision engines load only sha-pinned safetensors (`use_safetensors=True`, `local_files_only=True`), with no remote code, `.pth` or `save_pretrained`. The residual risk is accepted. A joint align and vision transformers bump (to 5.10 or later) is a follow-up.
- The trainer's `vram_mb` 11500 and the pyworker RSS against `mem_limit: 8g` during training are unmeasured, because they need weights.

## Security review notes
- Dataset archive (`train_dataset.py`): entries are flattened to base names, dotfiles and `_`-prefixed names are skipped, and only image and `.txt` files are kept. Duplicate names are rejected case-insensitively. Entry count, per-file size, total size and the 4–64 image count are all capped. Reading stops one byte past the header size, so a lying header is caught. Zip-slip and zip-bomb attempts are rejected. The download is capped at 512 MiB.
- Trainer child (`aitoolkit_train.py`): the argv is fixed and there is no shell. The environment comes from an allowlist (no bearer token path, credentials or proxies) plus the HF/transformers offline flags. The child runs in a new session with stdin closed. Cancellation and `max_minutes` (at most 120) kill the process group. It runs inside the pyworker container, which is offline, on the internal GPU network, non-root, with `cap_drop: ALL` and no `docker.sock`.
- Log streaming (`train_config.scrub_line`): the scrubber removes ANSI codes, URLs (which carry presigned signatures), `hf_`/`sk-` tokens and `authorization`/`token`/`password`/`secret`/`bearer` values. Lines are capped at 300 characters and at 2000 lines per job.
- Params: `trigger_word` is limited to `^[a-z][a-z0-9_]{1,31}$`, and steps, rank, learning rate and max_minutes are bounded. The job config is written as JSON, not YAML, so there is no injection.
- Vision: at most 32 references and 40 MiB per image are accepted. Decoding refuses images over 40 MP. Weights are safetensors only and offline. The DINOv2 `pooler_output` is the layer-normed CLS token, as the docstring says.
- Manifest: every weight file has a sha256 pin, every text file has a git blob pin, and revisions are pinned. Depth uses only the Apache-2.0 Small `-hf` checkpoint. The adapter's own repo, revision and licence are recorded per file.
- Tenant isolation and authorization are not affected in this part: the branch adds no API route, query or migration. Limiting the dataset to the tenant's approved refs and storing outputs as tenant assets is the Go wiring in the second part of the phase.

## Success criteria (this part)

| Criterion | Status |
|---|---|
| Manifest entries for the trainer base, DINOv2 and Depth-Anything-V2-Small, pinned and safetensors only | Met. The manifest lint passes and every file is pinned. Pulls are pending because the user paused model downloads |
| ai-toolkit engine served through the `Train` stream (progress, scrubbed logs, result) | Met for the engine code and tests. A real run is pending because the user paused model downloads, and because the train packaging needs a user decision (Medium 1) |
| DINOv2 `Score` and Depth-Anything-V2-Small `Depth` engines | Met for code and tests. GPU runs are pending because the user paused model downloads |
| Bench vision/train/train-smoke suites | Met against a fake worker. Real runs are pending because the user paused model downloads |
| `uv run pytest -m gpu -k "train or score or depth"` | Pending: the user paused model downloads |
| Two character LoRAs used, scenes scored, parallax enabled, benchmark report, sign-off, Ollama seeded, `/gpu` residency across cycles | Pending: deferred to the second part of this phase (needs the character, scene and render wiring and the seed-LLM-default migration), and the user paused model downloads |
| Security checklist: trainer offline, internal network, non-root, no docker.sock; weights pinned safetensors; pip-audit on extras | Met, with the pip-audit advisories accepted as residual risk (see Informational). The dataset tenancy item is deferred to the second part of this phase |

## Unresolved questions
1. How should the trainer be packaged? The options are a separate `train` extra with a `[tool.uv]` conflict declaration, or a second pyworker image built from the same Dockerfile. Adding ai-toolkit and its diffusers git pin from GitHub archives needs the user's explicit approval.
2. Should the transformers pin for align and vision be raised to 5.10 or later to clear the pip-audit advisories, and when?
