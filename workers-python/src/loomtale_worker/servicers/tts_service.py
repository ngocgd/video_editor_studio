"""Implements tts.proto's Synthesize streaming RPC. No engine is
registered before phase 9b, so every call honestly fails
engine_not_installed rather than faking a result.
"""

from __future__ import annotations

from loomtale.worker.v1 import tts_pb2, tts_pb2_grpc
from loomtale_worker.model_manager import (
    EngineNotInstalledError,
    GpuOomError,
    ModelManager,
    abort_engine_not_installed,
    abort_gpu_oom,
)


class TTSServicer(tts_pb2_grpc.TTSServicer):
    def __init__(self, manager: ModelManager) -> None:
        self._manager = manager

    async def Synthesize(self, request, context):  # noqa: N802
        try:
            await self._manager.load(request.engine)
        except EngineNotInstalledError:
            await abort_engine_not_installed(context, request.engine)
            return
        except GpuOomError as exc:
            await abort_gpu_oom(context, str(exc))
            return
        # Real synthesis (progress events, upload to output_put_url) is
        # implemented by the engine registered in phase 9b; reaching
        # here means an engine was somehow marked installed without a
        # run() implementation wired, which is itself a bug to surface.
        yield tts_pb2.SynthesizeEvent(result=tts_pb2.SynthesizeResult(output_key=""))


def register(server, manager: ModelManager) -> None:
    tts_pb2_grpc.add_TTSServicer_to_server(TTSServicer(manager), server)
