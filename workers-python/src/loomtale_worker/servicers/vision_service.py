"""Implements vision.proto's Score/Depth unary RPCs. No engine is
registered before phase 9c; see tts_service.py for the identical honesty
pattern.
"""

from __future__ import annotations

from loomtale.worker.v1 import vision_pb2, vision_pb2_grpc
from loomtale_worker.model_manager import (
    EngineNotInstalledError,
    GpuOomError,
    ModelManager,
    abort_engine_not_installed,
    abort_gpu_oom,
)


class VisionServicer(vision_pb2_grpc.VisionServicer):
    def __init__(self, manager: ModelManager) -> None:
        self._manager = manager

    async def Score(self, request, context):  # noqa: N802
        try:
            await self._manager.load(request.engine)
        except EngineNotInstalledError:
            await abort_engine_not_installed(context, request.engine)
            return None
        except GpuOomError as exc:
            await abort_gpu_oom(context, str(exc))
            return None
        return vision_pb2.ScoreResponse(score=0.0)

    async def Depth(self, request, context):  # noqa: N802
        try:
            await self._manager.load(request.engine)
        except EngineNotInstalledError:
            await abort_engine_not_installed(context, request.engine)
            return None
        except GpuOomError as exc:
            await abort_gpu_oom(context, str(exc))
            return None
        return vision_pb2.DepthResponse(output_key="")


def register(server, manager: ModelManager) -> None:
    vision_pb2_grpc.add_VisionServicer_to_server(VisionServicer(manager), server)
