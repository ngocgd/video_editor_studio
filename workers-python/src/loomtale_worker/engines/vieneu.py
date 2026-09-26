"""Vietnamese narration with VieNeu-TTS v3 Turbo (Apache-2.0) on ONNX
Runtime. The SDK's GPU path loads its audio codec with
trust_remote_code, which executes Python shipped inside a model
repository; this worker never does that, so it runs the torch-free ONNX
engine (CPU) with the codec's ONNX export instead. Voices are either
one of the SDK's built-in presets or a clone of the voice preset's
reference audio (consent checked by the servicer).
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import numpy as np

from loomtale_worker.engines.audio import join_with_pauses
from loomtale_worker.engines.hf_cache_view import build_view
from loomtale_worker.engines.jobs import SynthesisJob, SynthesisOutput
from loomtale_worker.engines.local_files import LocalFiles
from loomtale_worker.engines.text_chunks import chunk_text
from loomtale_worker.engines.threaded import InvalidJobError, ThreadedEngine, float_param

MODEL_DIR = "tts/vieneu-v3-turbo"
GRAPH_DIR = f"{MODEL_DIR}/onnx_update"
CODEC_DIR = "tts/moss-audio-tokenizer-nano-onnx"
CODEC_REPO = "OpenMOSS-Team/MOSS-Audio-Tokenizer-Nano-ONNX"
CODEC_REVISION = "ceff0d0749bfb3fa2d61149794ec6feef0d1e1ae"
CODEC_FILES = (
    "codec_browser_onnx_meta.json",
    "moss_audio_tokenizer_decode_full.onnx",
    "moss_audio_tokenizer_decode_shared.data",
    "moss_audio_tokenizer_decode_step.onnx",
    "moss_audio_tokenizer_encode.onnx",
    "moss_audio_tokenizer_encode.data",
)
FILES = (
    f"{MODEL_DIR}/speaker_encoder.onnx",
    f"{MODEL_DIR}/denoiser.onnx",
    f"{GRAPH_DIR}/config.json",
    f"{GRAPH_DIR}/tokenizer.json",
    f"{GRAPH_DIR}/vieneu_prefill.onnx",
    f"{GRAPH_DIR}/vieneu_decode_step.onnx",
    f"{GRAPH_DIR}/vieneu_acoustic_cached.onnx",
    f"{GRAPH_DIR}/vieneu_backbone_shared.data",
    f"{GRAPH_DIR}/vieneu_v3_heads.npz",
    *(f"{CODEC_DIR}/{name}" for name in CODEC_FILES),
)

# The SDK chunks internally at about 256 characters; feeding it about a
# paragraph at a time gives progress events without changing its output.
MAX_CHARS_PER_CALL = 1000
PAUSE_BETWEEN_CHUNKS_S = 0.3


def _hub_cache_root() -> Path:
    from huggingface_hub import constants

    return Path(constants.HF_HUB_CACHE)


class VieneuEngine(ThreadedEngine):
    name = "vieneu-v3-turbo"
    task = "tts"
    license = "Apache-2.0"

    def __init__(self, models_root: Path) -> None:
        self.files = LocalFiles(models_root, FILES)
        self._tts: Any = None

    def _load_sync(self) -> None:
        root = self.files.root
        build_view(
            _hub_cache_root(),
            CODEC_REPO,
            CODEC_REVISION,
            {name: root / CODEC_DIR / name for name in CODEC_FILES},
        )
        from vieneu import Vieneu

        self._tts = Vieneu(
            mode="v3turbo",
            backbone_repo=str(root / MODEL_DIR),
            backend="onnx",
            onnx_dir=str(root / GRAPH_DIR),
            device="cpu",
        )

    def _unload_sync(self) -> None:
        if self._tts is not None and hasattr(self._tts, "close"):
            self._tts.close()
        self._tts = None

    def _run_sync(self, job: SynthesisJob) -> SynthesisOutput:
        if job.language != "vi":
            raise InvalidJobError(
                f"vieneu-v3-turbo narrates Vietnamese, got language {job.language!r}"
            )
        temperature = float_param(job.params, "temperature", 0.8, 0.05, 2.0)
        voice: dict[str, Any] = {}
        if job.reference_wav is not None:
            voice["ref_audio"] = str(job.reference_wav)
        elif job.voice:
            voice["voice"] = job.voice
        chunks = chunk_text(job.text, MAX_CHARS_PER_CALL)
        waves: list[np.ndarray] = []
        for i, chunk in enumerate(chunks):
            waves.append(np.asarray(self._tts.infer(chunk, temperature=temperature, **voice)))
            job.progress(int((i + 1) * 100 / len(chunks)))
        rate = int(self._tts.sample_rate)
        return SynthesisOutput(
            samples=join_with_pauses(waves, rate, PAUSE_BETWEEN_CHUNKS_S), sample_rate=rate
        )
