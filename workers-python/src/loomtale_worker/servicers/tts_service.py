"""Implements tts.proto's Synthesize streaming RPC. No engine is
registered before phase 9b, so every call honestly fails
engine_not_installed rather than faking a result.
"""

from __future__ import annotations

import grpc

from loomtale.worker.v1 import tts_pb2_grpc
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
        # Reaching here means an engine reported itself installed and
        # loaded without a run() implementation wired (phase 9b adds
        # both together); aborting loudly is safer than faking a
        # zero-byte "success" result that a caller would store as real
        # output.
        await context.abort(
            grpc.StatusCode.UNIMPLEMENTED,
            f"engine {request.engine!r} has no Synthesize implementation wired",
        )
        return
        yield  # pragma: no cover - unreachable, makes this an async generator


def register(server, manager: ModelManager) -> None:
    tts_pb2_grpc.add_TTSServicer_to_server(TTSServicer(manager), server)
