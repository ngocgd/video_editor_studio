"""Implements train.proto's Train streaming RPC. No engine (ai-toolkit)
is registered before phase 9c; see tts_service.py for the identical
honesty pattern.
"""

from __future__ import annotations

from loomtale.worker.v1 import train_pb2, train_pb2_grpc
from loomtale_worker.model_manager import (
    EngineNotInstalledError,
    GpuOomError,
    ModelManager,
    abort_engine_not_installed,
    abort_gpu_oom,
)


class TrainServicer(train_pb2_grpc.TrainServicer):
    def __init__(self, manager: ModelManager) -> None:
        self._manager = manager

    async def Train(self, request, context):  # noqa: N802
        try:
            await self._manager.load(request.engine)
        except EngineNotInstalledError:
            await abort_engine_not_installed(context, request.engine)
            return
        except GpuOomError as exc:
            await abort_gpu_oom(context, str(exc))
            return
        yield train_pb2.TrainEvent(result=train_pb2.TrainResult(output_key=""))


def register(server, manager: ModelManager) -> None:
    train_pb2_grpc.add_TrainServicer_to_server(TrainServicer(manager), server)
