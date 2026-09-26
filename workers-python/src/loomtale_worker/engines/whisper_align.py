"""Subtitle alignment: faster-whisper large-v3 (CTranslate2) finds speech
and times recognised words; the known narration script is matched onto
them (transcript_match), so cues always carry the script's own text.
English is refined to word level by forcing each cue's characters onto
wav2vec2's frame probabilities (ctc_align); Vietnamese stays at cue
(segment) level, since no pinned Vietnamese character model exists.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import numpy as np

from loomtale_worker.engines.ctc_align import align_words
from loomtale_worker.engines.jobs import AlignJob, AlignOutput
from loomtale_worker.engines.local_files import LocalFiles
from loomtale_worker.engines.text_chunks import subtitle_cues
from loomtale_worker.engines.threaded import InvalidJobError, ThreadedEngine
from loomtale_worker.engines.transcript_match import RecognisedWord, build_cues, match_script

ASR_DIR = "align/faster-whisper-large-v3"
W2V_DIR = "align/wav2vec2-base-960h"
FILES = (
    f"{ASR_DIR}/model.bin",
    f"{ASR_DIR}/config.json",
    f"{ASR_DIR}/preprocessor_config.json",
    f"{ASR_DIR}/tokenizer.json",
    f"{ASR_DIR}/vocabulary.json",
    f"{W2V_DIR}/model.safetensors",
    f"{W2V_DIR}/config.json",
    f"{W2V_DIR}/preprocessor_config.json",
    f"{W2V_DIR}/vocab.json",
    f"{W2V_DIR}/tokenizer_config.json",
    f"{W2V_DIR}/special_tokens_map.json",
)
CTRANSLATE2_FILES = (f"{ASR_DIR}/model.bin",)

SAMPLE_RATE = 16_000
LANGUAGES = ("en", "vi")
# Audio around a cue's coarse times given to wav2vec2, so a word the
# recogniser timed slightly late is still inside the window.
CUE_PADDING_S = 0.25


def refine_cue_times(
    emissions: Any,  # noqa: ANN401 - callable(samples) -> (frames, vocab) log-probs
    audio: np.ndarray,
    text: str,
    coarse: list[tuple[float, float]],
    vocab: dict[str, int],
) -> list[tuple[float, float]]:
    """Replaces the coarse per-word times with word-level forced
    alignment, cue by cue. Words the aligner cannot place keep their
    coarse times."""
    words = text.split()
    refined = list(coarse)
    duration = len(audio) / SAMPLE_RATE
    i = 0
    for cue in subtitle_cues(text):
        count = len(cue.split())
        start = max(0.0, coarse[i][0] - CUE_PADDING_S)
        end = min(duration, coarse[i + count - 1][1] + CUE_PADDING_S)
        window = audio[int(start * SAMPLE_RATE) : int(end * SAMPLE_RATE)]
        if len(window) >= SAMPLE_RATE // 10:
            log_probs = emissions(window)
            frame_s = (len(window) / SAMPLE_RATE) / max(log_probs.shape[0], 1)
            spans = align_words(log_probs, words[i : i + count], vocab)
            for k, span in enumerate(spans):
                if span is not None:
                    refined[i + k] = (start + span[0] * frame_s, start + span[1] * frame_s)
        i += count
    return refined


class WhisperAlignEngine(ThreadedEngine):
    name = "whisper-align"
    task = "align"
    license = "MIT"

    def __init__(self, models_root: Path) -> None:
        self.files = LocalFiles(models_root, FILES, ctranslate2=CTRANSLATE2_FILES)
        self._asr: Any = None
        self._w2v: Any = None
        self._vocab: dict[str, int] = {}

    def _load_sync(self) -> None:
        from faster_whisper import WhisperModel
        from transformers import Wav2Vec2ForCTC

        root = self.files.root
        self._asr = WhisperModel(
            str(root / ASR_DIR), device="cuda", compute_type="float16", local_files_only=True
        )
        self._w2v = (
            Wav2Vec2ForCTC.from_pretrained(
                str(root / W2V_DIR), use_safetensors=True, local_files_only=True
            )
            .to("cuda")
            .eval()
        )
        self._vocab = json.loads((root / W2V_DIR / "vocab.json").read_text(encoding="utf-8"))

    def _unload_sync(self) -> None:
        self._asr = None
        self._w2v = None

    def _emissions(self, window: np.ndarray) -> np.ndarray:
        import torch

        # wav2vec2-base-960h expects zero-mean, unit-variance input.
        x = (window - window.mean()) / np.sqrt(window.var() + 1e-7)
        with torch.inference_mode():
            logits = self._w2v(torch.from_numpy(x).float().unsqueeze(0).to("cuda")).logits[0]
            return torch.log_softmax(logits.float(), dim=-1).cpu().numpy()

    def _run_sync(self, job: AlignJob) -> AlignOutput:
        if job.language not in LANGUAGES:
            raise InvalidJobError(f"alignment supports {LANGUAGES}, got {job.language!r}")
        if not job.text.split():
            raise InvalidJobError("alignment needs the narration text")
        from faster_whisper import decode_audio

        audio = decode_audio(str(job.audio_path), sampling_rate=SAMPLE_RATE)
        duration = len(audio) / SAMPLE_RATE
        segments, _info = self._asr.transcribe(
            audio,
            language=job.language,
            word_timestamps=True,
            vad_filter=True,
            condition_on_previous_text=False,
            beam_size=5,
        )
        recognised: list[RecognisedWord] = []
        for seg in segments:
            for w in seg.words or []:
                recognised.append(RecognisedWord(word=w.word.strip(), start=w.start, end=w.end))
            if duration > 0:
                job.progress(min(80, int(seg.end / duration * 80)))

        match = match_script(job.text.split(), recognised, duration)
        times = match.times
        word_level = job.language == "en"
        if word_level:
            times = refine_cue_times(self._emissions, audio, job.text, times, self._vocab)
        job.progress(100)
        return AlignOutput(
            language=job.language,
            granularity="word" if word_level else "segment",
            segments=build_cues(job.text, times, with_words=word_level),
            audio_duration_s=duration,
            matched_ratio=match.matched_ratio,
        )
