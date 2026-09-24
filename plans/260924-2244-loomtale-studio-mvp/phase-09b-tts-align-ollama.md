# Phase 09b: TTS (EN/VI), subtitle alignment, and the local Ollama LLM <!-- RT#15 split of the former phase 9 -->

## Context links
- [plan.md](plan.md) · [contract §6 model table, §10 machine check](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- [TTS research](../reports/researcher-260924-2145-narration-voice-tts-channels.md) · [local models on RTX 5060 Ti](../reports/researcher-260924-2128-local-ai-models-rtx5060ti.md)
- Depends on phase 9a (manifest, pull, bench harness, `compose.gpu.yml` ownership passes serially 9a → 9b → 9c). End-to-end steps need phase 7's voice and align steps. Runs in parallel with phase 8.

## Overview
- Priority: P1 · Status: pending · Effort: 20h
- This phase makes voice and subtitle alignment real in the Python worker, pulls the **required local default LLM** for Ollama, benchmarks it against the claude CLI, and calibrates the duration estimate from measured speech rates.

## Requirements
- TTS engines (pyworker extras `tts-en`, `tts-vi`, PyTorch cu128 wheels): Chatterbox (EN) and VieNeu-TTS v3 Turbo (VI), voice cloning from the preset reference audio (consent flag from phase 7 required). Excluded: VibeVoice, F5-TTS, viXTTS.
- Align engine (extra `align`): WhisperX / faster-whisper large-v3; EN word-level with wav2vec2, VI segment-level with text matching.
- All weights are manifest entries (full transitive list, safetensors/CTranslate2/GGUF only, sha256-pinned) pulled by the worker; pyworker runs offline (`HF_HUB_OFFLINE=1`) on `gpu_net`. <!-- RT#13 -->
- CUDA OOM inside an engine surfaces as `gpu_oom` (phase 3 full-unload + 1 retry). <!-- RT#5 -->
- **Ollama (required local default)** <!-- RT#7 -->: candidates Gemma 4 12B Q6_K or Qwen3.5-9B Q8 (licence re-verified; VRAM estimate ≤ `budget_mb`). The GGUF is a manifest entry pulled by the worker; Ollama imports it offline with a Modelfile (`FROM /models/<file>.gguf`, `ollama create`), because the `ollama` container has no egress. Residency is proven through `/api/ps`.
- Benchmark suites: `tts` (30 EN + 30 VI paragraphs: real-time factor, VRAM peak), `align` (a 5-min EN/VI audio: max drift), `llm` (tok/s, first-token latency, EN/VI outline and draft quality samples human-rated 1–5 vs claude CLI).
- Calibration: measured wpm per voice preset is written to `voice_presets.measured_wpm`; `duration.Estimate` uses it and drops the "uncalibrated" label.

## Related files
- Create: `workers-python/src/loomtale_worker/engines/{chatterbox,vieneu,whisper_align}.py`, `api/internal/bench/{tts,align,llm}_suite.go`, `models/ollama/*.Modelfile`.
- Modify: `models/manifest.yaml` (TTS, align, LLM entries), `deploy/docker/pyworker.Dockerfile` (extras), `deploy/compose.gpu.yml` (pyworker GPU reservation, ollama models mount).

## Implementation steps
1. Add the manifest entries; pull and verify.
2. Implement the Chatterbox and VieNeu engines (clone from preset reference audio); run a voice step end to end from the storyboard.
3. Implement the align engine (EN word-level, VI segment-level); run align end to end.
4. Import the Ollama model offline; run `/settings/llm/test` with Ollama; run the **Ollama variants** of the phase 6 and phase 7 success criteria and record results for the 9c report.
5. Run the tts, align and llm bench suites; record rows and the human ratings.
6. Calibrate wpm per voice preset.

## Todo checklist
- [ ] Manifest entries + pulls
- [ ] TTS EN/VI engines end to end
- [ ] Align engine EN/VI end to end
- [ ] Ollama model imported offline; phase 6/7 Ollama variants run
- [ ] tts/align/llm suites + ratings
- [ ] wpm calibration

## Performance budget checks
- TTS real-time factor ≤0.3; align ≤10 min per hour of audio; LLM first token ≤3s when resident; residency switch ≤15s. Revise with measured data.
- pyworker RSS ≤8g (its `mem_limit`); VRAM peak ≤ `budget_mb`.

## Security checklist
- [ ] Voice clone references require the phase 7 consent flag
- [ ] pyworker and ollama offline; weights pinned; pickles refused
- [ ] `pip-audit` on engine extras

## Tests
- `cd workers-python && uv run pytest -q -m "not gpu"` (param validation); `uv run pytest -m gpu` on the host GPU.
- `loomtale bench --suite voice-smoke` (1 EN line, 1 VI line, 1 align, 1 residency switch Ollama ↔ TTS).

## Success criteria
- EN and VI voice and aligned subtitles are produced for a real episode.
- Ollama answers AI actions with residency proven by `/api/ps`, and the phase 6/7 Ollama variants pass (or their gaps are recorded for the 9c decision).
- Predicted vs actual episode duration differ by ≤10% after calibration.

## Risks + rollback
- A TTS engine fails on `sm_120` or VI quality is insufficient (Medium×High): per-engine smoke, fallback from contract §6 (e.g. Orpheus for EN) as a user decision.
- The local LLM's VI long-form quality is poor (Medium×Medium): Ollama stays the default for actions where it rates ≥ the agreed bar; per-action overrides point other actions to claude CLI (recorded in 9c sign-off).
- Dependency conflicts between TTS and align extras (Medium×Medium): separate uv extras, locked; if unresolvable, a second pyworker instance of the same image with another extra (still gRPC, still offline).
- Rollback: `loomtale models remove`; unregister engines by config.

## Next steps
Phase 9c adds LoRA training, scoring, depth, the benchmark report and default-model sign-off.
