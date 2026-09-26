"""Depth maps for parallax with Depth-Anything-V2-Small (Apache-2.0; the
Base and Large checkpoints are non-commercial and never used). The
prediction is resized to the input image and written as a 16-bit
grayscale PNG. Loaded from the pinned safetensors through transformers,
offline.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from loomtale_worker.engines.jobs import DepthJob, DepthOutput
from loomtale_worker.engines.local_files import LocalFiles
from loomtale_worker.engines.threaded import ThreadedEngine
from loomtale_worker.engines.vision_images import load_rgb
from loomtale_worker.engines.vision_math import depth_to_uint16, gray16_png

WEIGHTS_DIR = "vision/depth-anything-v2-small"
FILES = (
    f"{WEIGHTS_DIR}/model.safetensors",
    f"{WEIGHTS_DIR}/config.json",
    f"{WEIGHTS_DIR}/preprocessor_config.json",
)


class DepthSmallEngine(ThreadedEngine):
    name = "depth-anything-v2-small"
    task = "vision"
    license = "Apache-2.0"

    def __init__(self, models_root: Path) -> None:
        self.files = LocalFiles(models_root, FILES)
        self._model: Any = None
        self._processor: Any = None

    def _load_sync(self) -> None:
        from transformers import AutoImageProcessor, AutoModelForDepthEstimation

        root = str(self.files.root / WEIGHTS_DIR)
        self._processor = AutoImageProcessor.from_pretrained(root, local_files_only=True)
        self._model = (
            AutoModelForDepthEstimation.from_pretrained(
                root, use_safetensors=True, local_files_only=True
            )
            .to("cuda")
            .eval()
        )

    def _unload_sync(self) -> None:
        self._model = None
        self._processor = None

    def _run_sync(self, job: DepthJob) -> DepthOutput:
        import torch

        image = load_rgb(job.image_path)
        width, height = image.size
        inputs = self._processor(images=image, return_tensors="pt").to("cuda")
        with torch.inference_mode():
            predicted = self._model(**inputs).predicted_depth
        # Upsample the (1, h, w) prediction to the input size.
        depth = torch.nn.functional.interpolate(
            predicted.unsqueeze(1).float(),
            size=(height, width),
            mode="bicubic",
            align_corners=False,
        )[0, 0]
        pixels = depth_to_uint16(depth.cpu().numpy())
        return DepthOutput(png=gray16_png(pixels), width=width, height=height)
