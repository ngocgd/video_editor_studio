"""Implements vision.proto's Score/Depth unary RPCs.

Score params:
- reference_urls (required): presigned GETs of the character's approved
  reference images, one per line (a URL never contains a newline).

Depth params:
- output_key: echoed back in the response (the storage key the Go side
  presigned output_put_url for).

The engine is made resident before any image is downloaded, so a missing
engine fails fast as engine_not_installed (see streaming.preload).
"""

from __future__ import annotations

import tempfile
import time
from pathlib import Path

from loomtale.worker.v1 import vision_pb2, vision_pb2_grpc
from loomtale_worker.engines.dinov2_score import MAX_REFERENCES
from loomtale_worker.engines.jobs import DepthJob, DepthOutput, ScoreJob, ScoreOutput
from loomtale_worker.engines.threaded import cuda_peak_mb_and_reset
from loomtale_worker.model_manager import ModelManager
from loomtale_worker.servicers.streaming import (
    abort_invalid,
    fetch_input,
    finish_job,
    preload,
    push_output,
)

# A generated scene or an uploaded ref is a few MB at most.
MAX_IMAGE_BYTES = 40 * 1024 * 1024


def reference_urls(params: dict[str, str]) -> list[str]:
    return [u.strip() for u in params.get("reference_urls", "").splitlines() if u.strip()]


class VisionServicer(vision_pb2_grpc.VisionServicer):
    def __init__(self, manager: ModelManager) -> None:
        self._manager = manager

    async def Score(self, request, context):  # noqa: N802
        params = dict(request.params)
        refs = reference_urls(params)
        if not request.image_get_url:
            await abort_invalid(context, "image_get_url is required")
        if not refs or len(refs) > MAX_REFERENCES:
            await abort_invalid(
                context, f"params.reference_urls must list 1 to {MAX_REFERENCES} images"
            )

        await preload(context, self._manager, request.engine)
        with tempfile.TemporaryDirectory(prefix="score-") as tmp:
            image = Path(tmp) / "image"
            image.write_bytes(await fetch_input(context, request.image_get_url, MAX_IMAGE_BYTES))
            ref_paths = []
            for i, url in enumerate(refs):
                p = Path(tmp) / f"ref-{i}"
                p.write_bytes(await fetch_input(context, url, MAX_IMAGE_BYTES))
                ref_paths.append(p)
            job = ScoreJob(image_path=image, reference_paths=ref_paths, params=params)
            cuda_peak_mb_and_reset()
            started = time.monotonic()
            out: ScoreOutput = await finish_job(
                context, request.engine, self._manager.run(request.engine, job)
            )
            elapsed = time.monotonic() - started
        return vision_pb2.ScoreResponse(
            score=out.score,
            metadata={
                **out.metadata,
                "engine": request.engine,
                "score_s": f"{elapsed:.3f}",
                "vram_peak_mb": str(cuda_peak_mb_and_reset()),
            },
        )

    async def Depth(self, request, context):  # noqa: N802
        params = dict(request.params)
        if not request.image_get_url:
            await abort_invalid(context, "image_get_url is required")
        if not request.output_put_url:
            await abort_invalid(context, "output_put_url is required")

        await preload(context, self._manager, request.engine)
        with tempfile.TemporaryDirectory(prefix="depth-") as tmp:
            image = Path(tmp) / "image"
            image.write_bytes(await fetch_input(context, request.image_get_url, MAX_IMAGE_BYTES))
            job = DepthJob(image_path=image, params=params)
            out: DepthOutput = await finish_job(
                context, request.engine, self._manager.run(request.engine, job)
            )
        await push_output(context, request.output_put_url, out.png, "image/png")
        return vision_pb2.DepthResponse(output_key=params.get("output_key", ""))


def register(server, manager: ModelManager) -> None:
    vision_pb2_grpc.add_VisionServicer_to_server(VisionServicer(manager), server)
