# Phase 09c: LoRA trainer (pyworker engine), consistency scoring, depth, benchmark sign-off <!-- RT#15 split of the former phase 9 -->

## Context links
- [plan.md](plan.md) · [contract §2 AC1 (≥2 consistent characters), §6 model table](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- [model research §3 character consistency](../reports/researcher-260924-2128-local-ai-models-rtx5060ti.md)
- Depends on phase 9b (serial ownership of `compose.gpu.yml`, `models/manifest.yaml`, `pyworker.Dockerfile`) and phase 7 (`character_loras`, refs). Runs in parallel with phase 8 (pact below).

## Overview
- Priority: P1 · Status: pending · Effort: 22h
- This phase makes LoRA training, character-consistency scoring and depth real, all inside the Python worker under the same residency manager, then produces the benchmark report, the default-model decision table and the user sign-off, and seeds Ollama as the tenant default LLM.

## Requirements
- **LoRA trainer as a pyworker engine** <!-- RT#5 RT#13 -->: ai-toolkit (licence verified before use) is an `Engine` with task `train`, served through the phase 4 `train.proto` `Train` stream. It runs inside pyworker, so it is covered by `ModelManager` residency and the single GPU slot; there is no separate trainer container, no `docker compose run` orchestration and no `docker.sock`. Training steps run at priority 4 with the phase 3 `train.lora` timeout (2h). Dataset: 20–24 approved refs per character, read through internal presigned URLs; weights written back as an asset and a `character_loras` version.
- Base model for training: the phase 9a scene model (Z-Image Turbo) — the trainer's base weights and configs are manifest entries (safetensors only, offline).
- Consistency scorer: DINOv2 (Apache-2.0) via `vision.proto` `Score`; the `image.score` step writes `scene_takes.params.score` (cosine similarity against the character's approved refs).
- Depth for parallax: Depth-Anything-V2-**Small** only (Apache-2.0; Base and Large are non-commercial) via `vision.proto` `Depth`; `/gpu` `capabilities` gains `depth` once installed (schema owned by phase 3).
- Benchmark report `plans/reports/benchmark-<date>-models.md`: all suites from 9a–9c (s/img, VRAM peak, RSS peak, TTS RTF, align drift, LLM tok/s and ratings, residency switch, LoRA minutes), the Ollama variant results for phases 6 and 7, and a default-model decision table for the user.
- **Default seeding** <!-- RT#7 -->: after sign-off, a data migration (idempotent, per tenant) sets the `llm_settings` default to Ollama with the signed-off model, plus any per-action overrides the user chose.

## Architecture
**Pact with phase 8:** `image.score` writes `scene_takes.params.score`; `image.depth` produces the depth asset; `/gpu` reports `depth`, which unlocks parallax in phase 8. Phase 9c never edits phase 8 files.

## Related files
- Create: `workers-python/src/loomtale_worker/engines/{aitoolkit_train,depth_small,dinov2_score}.py`, `api/internal/bench/{train,vision}_suite.go`, `api/internal/scenes/steps_score_depth.go` (handlers registered by the lead), `db/migrations/*_seed_llm_default.sql`.
- Modify: `models/manifest.yaml`, `deploy/docker/pyworker.Dockerfile` (extras `train`, `vision`), `deploy/compose.gpu.yml` (pyworker `mem_limit` 8g confirmed).

## Implementation steps
1. Add manifest entries (ai-toolkit base weights, DINOv2, Depth-Anything-V2-Small); pull and verify.
2. Implement the ai-toolkit engine (`Train` stream: progress, log lines scrubbed, result) and the `train.lora` step; train 1 character LoRA and use it in a scene.
3. Implement the DINOv2 score and depth-small engines and their steps; confirm the parallax unlock.
4. Train the second recurring character (AC1 needs ≥2) and score an episode's scenes.
5. Run all suites, write the benchmark report with the Ollama variants, and present the default-model decision table for sign-off.
6. Apply the default seeding migration per the sign-off.

## Todo checklist
- [ ] Manifest entries + pulls
- [ ] ai-toolkit pyworker engine + LoRA step + first character
- [ ] DINOv2 score + depth small + parallax unlock
- [ ] Second character LoRA + episode scored
- [ ] Benchmark report + user sign-off
- [ ] Ollama default seeded

## Performance budget checks
- LoRA ≤60 min per character; pyworker RSS ≤8g during training; VRAM ≤ `budget_mb`.
- No OOM across 3 consecutive image → voice → align → LLM → score cycles.
- End-to-end throughput extrapolated from stage timings within the contract §3 estimate (≤3.5h machine time per 1h video); measured on a 30-min episode in phase 12.

## Security checklist
- [ ] Trainer inside pyworker: offline, internal network, non-root, no `docker.sock`
- [ ] Training dataset limited to approved refs of the tenant; outputs stored as tenant assets
- [ ] Trainer and vision weights pinned (safetensors only); `pip-audit` on extras

## Tests
- `uv run pytest -m gpu -k "train or score or depth"` on the host GPU.
- `loomtale bench --suite train-smoke` (a 50-step LoRA on 4 refs, then a score).
- `scripts/tb.ps1 test-integration -run TestSeedLLMDefault` (idempotent seeding).

## Success criteria
- Two character LoRAs exist and are used; scenes carry consistency scores; parallax is enabled.
- The benchmark report exists and the user has signed off the default models; Ollama is the seeded default LLM.
- `/gpu` shows one resident model at a time across the cycles (app-side probes).

## Risks + rollback
- LoRA quality insufficient for AC1 (Medium×High): Qwen-Image-Edit ref-based edits (phase 9a) as a second path; paid character sheets as a contract §6 fallback (user decision).
- ai-toolkit dependencies conflict with TTS extras (Medium×Medium): separate extra; if unresolvable, a second pyworker instance of the same image with the `train` extra (gRPC, offline, same residency; never docker.sock).
- Rollback: `loomtale models remove`; the seeding migration's down restores the previous default.

## Next steps
Phase 10 uses Qwen-Image thumbnails (9a) and QC scores. Phase 12 runs the full acceptance episodes.
