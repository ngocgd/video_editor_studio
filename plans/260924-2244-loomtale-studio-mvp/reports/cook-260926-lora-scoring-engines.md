# Phase 9c, part 1: consistency scoring, depth and the LoRA trainer engine

Branch `feat/lora-scoring-engines`, worktree `.claude/worktrees/lane-b-9c1`. This is the first of two parts. It builds everything in phase 9c that does not need phase 7 (characters and scenes are not on main yet). The feature is inert on main without the second part: nothing in the API calls the new engines yet, apart from the benchmark command.

## What shipped

- **Manifest entries** (`models/manifest.yaml` and the embedded copy `api/internal/models/assets/manifest.yaml`), pinned from Hugging Face metadata only, no weights downloaded:
  - `dinov2-base` (task `score`, engine pyworker), safetensors only.
  - `depth-anything-v2-small` (task `depth`, engine pyworker), safetensors only.
  - `z-image-turbo-trainer` (task `lora`, engine pyworker): the 19 diffusers-format files of `Tongyi-MAI/Z-Image-Turbo@f332072a` under `train/z-image-turbo/`, plus the v2 training adapter from `ostris/zimage_turbo_training_adapter@654cd1bf` (Apache-2.0) under `train/z-image-turbo/training-adapter/`. The planned VRAM of 11500 MB (qfloat8 with low-VRAM mode) is not measured yet.
  - `api/internal/models/manifest_test.go` lists the new names.
- **pyworker engines** (`workers-python/src/loomtale_worker/engines/`):
  - `dinov2_score.py`: DINOv2 CLS embeddings; a render's score is its mean cosine similarity to the reference set (`vision_math.py`, `vision_images.py`).
  - `depth_small.py`: Depth-Anything-V2-Small, a 16-bit grayscale PNG depth map.
  - `aitoolkit_train.py` with `train_config.py` and `train_dataset.py`: runs ai-toolkit as a child process (`python run.py job.json` from `AI_TOOLKIT_DIR`, default `/opt/ai-toolkit`) with offline environment variables. Cancel and `max_minutes` kill the whole process group. The dataset zip is validated: flat, images plus optional `.txt` captions, 4 to 64 images. Defaults: 1500 steps (10 to 4000), rank 16 (4 to 64), learning rate 1e-4, trigger word `loomtale_character`, 120 minutes. The output is the single final LoRA `.safetensors`.
  - All three import torch, transformers or diffusers lazily inside `load()`. When the runtime or ai-toolkit is missing they answer `engine_not_installed` honestly instead of failing at import.
  - `servicers/vision_service.py` serves real `Score` and `Depth` calls; `servicers/train_service.py` serves the real `Train` stream (progress, cancel, upload of the LoRA).
- **pyworker packaging**: a `vision` extra in `workers-python/pyproject.toml` (torch 2.9.1 cu128, transformers 5.2.0, the same pins as the merged `align` extra, and pillow >= 12.3.0), `uv.lock` regenerated, the Dockerfile comment lists `vision`. No image was built.
- **Benchmark suites** (`api/internal/bench/vision_suite.go`, `train_suite.go`, wired into `loomtale bench` through `api/cmd/loomtale/bench.go` and `bench_voice.go`, and into `scripts/bench-voice.sh`):
  - `vision`: leave-one-out DINOv2 score for each reference image (up to 32 references) and a depth map for each one, saved as `depth-<stem>.png`, with the one-resident-model switch budget checked.
  - `train`: a full LoRA run (default 1500 steps) on all references, budget 60 minutes per LoRA.
  - `train-smoke`: a 50-step LoRA on four references, then a score of a held-out reference against those four; the time budget is extrapolated to 1500 steps.
  - Inputs come from a new `--refs` directory (png, jpg, jpeg or webp, at most 64 files of 40 MB each), mounted read-only at `/refs` by the script. Each case writes one `model_benchmarks` row.
  - The README benchmark paragraph documents the new suites.
- **Tests**: `workers-python/tests/test_vision.py`, `test_train.py`, updated `test_engine_catalog.py`, and GPU tests in `test_engines_gpu.py` that skip without weights; `api/internal/bench/vision_train_suites_test.go` runs the suites against a fake Vision and Train gRPC worker.

## Verification

- Go (host toolchain 1.26.8, official zip with verified sha256): `go vet ./...` clean, `go test -count=1 ./...` all packages ok, including the eight new bench tests and the manifest test.
- golangci-lint 2.14.0 (official Windows release, sha256 checked against the release checksums; the Linux sum equals the toolbox pin): `0 issues` on the whole `api` module.
- gofmt: none of this branch's files are listed.
- Generated code: the branch touches no OpenAPI, proto or SQL sources, so there is nothing to regenerate. `models/manifest.yaml` equals the embedded copy.
- Python (host): `ruff check` and `ruff format --check` clean; `pytest`: 102 passed, 5 skipped (the GPU tests, which need weights).
- pip-audit on the exported `vision` requirements: 9 advisories, all in torch 2.9.1 and transformers 5.2.0, the versions the merged `align` extra already ships (the `.pth` unpickler, TorchScript and LSTM issues, config.json remote code before transformers 5.3, LightGlue, and a `save_pretrained` path traversal before 5.10). The vision engines load only sha-pinned safetensors, never `.pth` files or remote code, and never call `save_pretrained`, so these are recorded as accepted residual risk.
- Toolbox, integration and e2e (independent verify round, Docker back, base main 4962ecd): `scripts/tb.sh gen lint test` exited 0 with no generated-code drift, golangci-lint 0 issues, all Go packages passed with `-race`, and pytest reported 102 passed and 5 skipped (GPU tests that need weights). Web typecheck, lint, tests and bundle budget were green. Integration under the heavy lock (compose project `loomtale-b`) passed with 75 PASS lines and no FAIL or SKIP, and Playwright e2e passed 3 of 3; both stacks were brought down with `down -v`. Details are in `plans/reports/code-reviewer-260926-2125-lora-scoring-engines-review.md`.

## Success criteria

| Criterion | Status |
|---|---|
| Two character LoRAs exist and are used | Pending: deferred to the second part of this phase (needs phase 7 characters and scenes). The trainer engine itself exists; a real run is also pending because model downloads are paused by the user and the train packaging awaits approval (below). |
| Scenes carry consistency scores; parallax is enabled | Pending: deferred to the second part of this phase (needs phase 7 scenes for `steps_score_depth.go` and the `/gpu` depth capability). |
| Benchmark report exists and the user signed off the default models | Pending: deferred to the second part of this phase (needs phase 7), and model downloads are paused by the user. The benchmark suites that produce it are built. |
| Ollama is the seeded default LLM | Pending: deferred to the second part of this phase (the seed migration belongs there). |
| `/gpu` shows one resident model at a time across the cycles | Pending: model downloads paused by the user. The `vision` suite already checks the residency switch budget. |
| `pytest -m gpu -k "train or score or depth"`, `loomtale bench --suite train-smoke` | Pending: model downloads paused by the user. |
| Security: trainer inside pyworker, offline, internal network, non-root | Done by construction: the trainer is a child of pyworker with offline variables; no new container or socket. |
| Security: weights pinned, safetensors only, pip-audit on extras | Done for `vision` (above); the `train` extra does not exist yet. |

## Deviations

- **Train packaging is not done.** Adding a `train` extra (ai-toolkit and diffusers from pinned GitHub archive URLs, with a uv conflict declaration against the TTS and align extras) was refused by the permission classifier as untrusted code integration, so it waits for the user's explicit approval. Without it the trainer answers `engine_not_installed`, which is safe on main. Facts for the decision: ai-toolkit at commit 60d0c28 needs transformers 5.5.3, huggingface_hub 1.23.0 and diffusers at commit c943837, which clash with chatterbox-tts (diffusers 0.29) and the align extra (transformers 5.2.0), so it needs a separate conflicting extra or a second pyworker image. Its README targets torch 2.13 cu130, against our 2.9.1 cu128. Archive sha256: ai-toolkit 60d0c28 tar.gz `e1d4571ed81fa5b804ca39d089ea7f7ee70b33eaa25659d5d77c1a3d0e0ff329` (35.3 MB), diffusers c943837 tar.gz `3f030085ae92468e67b197437fd9de3227b58b26ed0f77c97b4d7f04da09b02c` (10.7 MB).
- The bench vision and train suites live on the existing voice runner (the same pyworker, residency and sink plumbing as TTS and align) instead of a new runner type.
- `train-smoke` scores a held-out reference against the training set. Scoring a render made with the new LoRA needs the LoRA render wiring (second part and phase 8).
- pyworker memory during training is measured by the host `docker stats` sampler in `scripts/bench-voice.sh`, next to `mem_limit` 8g.

## Deferred to the second part

`api/internal/scenes/steps_score_depth.go`, the `character_loras` and scenes wiring, the `/gpu` capability `depth`, the seed-default-LLM migration, the model sign-off and the benchmark report.

## Unresolved questions

- Does the user approve the ai-toolkit and diffusers source dependency (pinned archives, a separate conflicting extra or a second pyworker image)?
- Should the align and vision extras move together to transformers 5.10 or later to clear the remaining advisories?
- Will pyworker's 8g memory limit hold while ai-toolkit loads the bf16 transformer shards for qfloat8? This is only measurable on the GPU run.
