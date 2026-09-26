"""English narration with Chatterbox (MIT), cloning the voice preset's
reference audio. Loaded from the pinned safetensors only: the repo's
built-in voice (conds.pt) is a pickle and is never downloaded, so every
request must carry a reference clip, which the servicer only passes on
when the preset's consent flag is set.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from loomtale_worker.engines.audio import join_with_pauses
from loomtale_worker.engines.jobs import SynthesisJob, SynthesisOutput
from loomtale_worker.engines.local_files import LocalFiles
from loomtale_worker.engines.text_chunks import chunk_text
from loomtale_worker.engines.threaded import InvalidJobError, ThreadedEngine, float_param, int_param

WEIGHTS_DIR = "tts/chatterbox"
FILES = (
    f"{WEIGHTS_DIR}/ve.safetensors",
    f"{WEIGHTS_DIR}/t3_cfg.safetensors",
    f"{WEIGHTS_DIR}/s3gen.safetensors",
    f"{WEIGHTS_DIR}/tokenizer.json",
)

# Chatterbox generates at most 1000 speech tokens (about 40 s) per call;
# about 280 characters stays well inside that.
MAX_CHARS_PER_CALL = 280
PAUSE_BETWEEN_CHUNKS_S = 0.25


class ChatterboxEngine(ThreadedEngine):
    name = "chatterbox"
    task = "tts"
    license = "MIT"

    def __init__(self, models_root: Path) -> None:
        self.files = LocalFiles(models_root, FILES)
        self._model: Any = None

    def _load_sync(self) -> None:
        from chatterbox.tts import ChatterboxTTS

        self._model = ChatterboxTTS.from_local(self.files.root / WEIGHTS_DIR, device="cuda")

    def _unload_sync(self) -> None:
        self._model = None

    def _run_sync(self, job: SynthesisJob) -> SynthesisOutput:
        if job.language != "en":
            raise InvalidJobError(
                f"chatterbox narrates English only, got language {job.language!r}"
            )
        if job.reference_wav is None:
            raise InvalidJobError(
                "chatterbox clones a voice preset's reference audio; none was given"
            )
        import torch

        exaggeration = float_param(job.params, "exaggeration", 0.5, 0.0, 2.0)
        cfg_weight = float_param(job.params, "cfg_weight", 0.5, 0.0, 1.0)
        temperature = float_param(job.params, "temperature", 0.8, 0.05, 2.0)
        torch.manual_seed(int_param(job.params, "seed", 0, 0, 2**31 - 1))

        model = self._model
        model.prepare_conditionals(str(job.reference_wav), exaggeration=exaggeration)
        chunks = chunk_text(job.text, MAX_CHARS_PER_CALL)
        waves = []
        for i, chunk in enumerate(chunks):
            wav = model.generate(
                chunk, exaggeration=exaggeration, cfg_weight=cfg_weight, temperature=temperature
            )
            waves.append(wav.squeeze(0).detach().cpu().numpy())
            job.progress(int((i + 1) * 100 / len(chunks)))
        return SynthesisOutput(
            samples=join_with_pauses(waves, model.sr, PAUSE_BETWEEN_CHUNKS_S), sample_rate=model.sr
        )
