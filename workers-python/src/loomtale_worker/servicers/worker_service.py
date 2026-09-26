"""Implements worker.proto's control-plane RPCs: Health, ListEngines,
LoadModel, UnloadModel, GpuStatus.
"""

from __future__ import annotations

import grpc

from loomtale.worker.v1 import worker_pb2, worker_pb2_grpc
from loomtale_worker import gpu
from loomtale_worker.model_manager import (
    EngineNotInstalledError,
    GpuOomError,
    ModelManager,
    abort_engine_not_installed,
    abort_gpu_oom,
)


class WorkerServicer(worker_pb2_grpc.WorkerServicer):
    def __init__(self, manager: ModelManager) -> None:
        self._manager = manager

    async def Health(self, request, context):  # noqa: N802 - grpc method naming
        del request, context
        return worker_pb2.HealthResponse(ok=True)

    async def ListEngines(self, request, context):  # noqa: N802
        del request, context
        engines = [
            worker_pb2.EngineInfo(
                name=e.name,
                task=e.task,
                license=e.license,
                installed=e.installed(),
                loaded=(self._manager.resident_name == e.name),
                vram_held_mb=(
                    self._manager.resident_vram_mb if self._manager.resident_name == e.name else 0
                ),
            )
            for e in self._manager.registry.list()
        ]
        return worker_pb2.ListEnginesResponse(engines=engines)

    async def LoadModel(self, request, context):  # noqa: N802
        try:
            held_mb = await self._manager.load(request.engine)
        except EngineNotInstalledError as exc:
            # The message says why (unknown engine, weights missing, or
            # the image lacks the engine's runtime).
            await abort_engine_not_installed(context, str(exc) or request.engine)
        except GpuOomError as exc:
            await abort_gpu_oom(context, str(exc))
        return worker_pb2.LoadModelResponse(loaded=True, vram_held_mb=held_mb)

    async def UnloadModel(self, request, context):  # noqa: N802
        del context
        await self._manager.unload(request.engine or None)
        return worker_pb2.UnloadModelResponse(unloaded=True)

    async def GpuStatus(self, request, context):  # noqa: N802
        del request, context
        snap = gpu.probe()
        return worker_pb2.GpuStatusResponse(
            gpu_present=snap.present,
            total_mb=snap.total_mb,
            free_mb=snap.free_mb,
            resident_engine=self._manager.resident_name or "",
        )


def register(server: grpc.aio.Server, manager: ModelManager) -> None:
    worker_pb2_grpc.add_WorkerServicer_to_server(WorkerServicer(manager), server)
