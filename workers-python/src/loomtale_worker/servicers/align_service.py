"""Implements align.proto's Align streaming RPC. No engine is registered
before phase 9b; see tts_service.py for the identical honesty pattern.
"""

from __future__ import annotations

from loomtale.worker.v1 import align_pb2, align_pb2_grpc
from loomtale_worker.model_manager import (
    EngineNotInstalledError,
    GpuOomError,
    ModelManager,
    abort_engine_not_installed,
    abort_gpu_oom,
)


class AlignServicer(align_pb2_grpc.AlignServicer):
    def __init__(self, manager: ModelManager) -> None:
        self._manager = manager

    async def Align(self, request, context):  # noqa: N802
        try:
            await self._manager.load(request.engine)
        except EngineNotInstalledError:
            await abort_engine_not_installed(context, request.engine)
            return
        except GpuOomError as exc:
            await abort_gpu_oom(context, str(exc))
            return
        yield align_pb2.AlignEvent(result=align_pb2.AlignResult(output_key=""))


def register(server, manager: ModelManager) -> None:
    align_pb2_grpc.add_AlignServicer_to_server(AlignServicer(manager), server)
