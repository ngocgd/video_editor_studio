"""Character-consistency scoring with DINOv2 (Apache-2.0): the image and
each approved reference are embedded (the CLS token of the last layer,
after the final layer norm) and the score is the mean cosine similarity
of the image to the references. Loaded from the pinned safetensors only;
the repository's pytorch_model.bin is never fetched.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from loomtale_worker.engines.jobs import ScoreJob, ScoreOutput
from loomtale_worker.engines.local_files import LocalFiles
from loomtale_worker.engines.threaded import InvalidJobError, ThreadedEngine
from loomtale_worker.engines.vision_images import load_rgb
from loomtale_worker.engines.vision_math import similarity_to_references

WEIGHTS_DIR = "vision/dinov2-base"
FILES = (
    f"{WEIGHTS_DIR}/model.safetensors",
    f"{WEIGHTS_DIR}/config.json",
    f"{WEIGHTS_DIR}/preprocessor_config.json",
)

# Phase-7 characters keep 20-24 approved refs; a little headroom.
MAX_REFERENCES = 32
# Refs are embedded in batches of this size to bound peak VRAM.
BATCH = 8


class Dinov2ScoreEngine(ThreadedEngine):
    name = "dinov2-base"
    task = "vision"
    license = "Apache-2.0"

    def __init__(self, models_root: Path) -> None:
        self.files = LocalFiles(models_root, FILES)
        self._model: Any = None
        self._processor: Any = None

    def _load_sync(self) -> None:
        import torch
        from transformers import AutoImageProcessor, AutoModel

        root = str(self.files.root / WEIGHTS_DIR)
        self._processor = AutoImageProcessor.from_pretrained(root, local_files_only=True)
        self._model = (
            AutoModel.from_pretrained(root, use_safetensors=True, local_files_only=True)
            .to("cuda", dtype=torch.float16)
            .eval()
        )

    def _unload_sync(self) -> None:
        self._model = None
        self._processor = None

    def _embed(self, paths: list[Path]) -> Any:  # noqa: ANN401 - numpy (N, D)
        import numpy as np
        import torch

        chunks = []
        for i in range(0, len(paths), BATCH):
            images = [load_rgb(p) for p in paths[i : i + BATCH]]
            inputs = self._processor(images=images, return_tensors="pt").to(
                "cuda", dtype=torch.float16
            )
            with torch.inference_mode():
                out = self._model(**inputs)
            chunks.append(out.pooler_output.float().cpu().numpy())
        return np.concatenate(chunks, axis=0)

    def _run_sync(self, job: ScoreJob) -> ScoreOutput:
        if not job.reference_paths:
            raise InvalidJobError("scoring needs at least one reference image")
        if len(job.reference_paths) > MAX_REFERENCES:
            raise InvalidJobError(f"at most {MAX_REFERENCES} reference images are accepted")
        embeddings = self._embed([job.image_path, *job.reference_paths])
        mean, lo, hi = similarity_to_references(embeddings[0], embeddings[1:])
        return ScoreOutput(
            score=mean,
            metadata={
                "metric": "dinov2_cls_cosine_mean",
                "min": f"{lo:.4f}",
                "max": f"{hi:.4f}",
                "references": str(len(job.reference_paths)),
            },
        )
