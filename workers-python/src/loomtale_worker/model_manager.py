"""ModelManager owns the single-resident-engine invariant this process
enforces: only one engine's weights are ever loaded at a time (the whole
point of the phase 3/4 residency contract), guarded by one asyncio.Lock
so concurrent RPCs never race a load/unload.
"""

from __future__ import annotations

import asyncio
import logging

import grpc

from loomtale_worker.engines.registry import EngineRegistry

logger = logging.getLogger(__name__)


class EngineNotInstalledError(Exception):
    """Raised when the requested engine name is unknown or its weights
    are not present on disk; servicers translate this to
    FAILED_PRECONDITION engine_not_installed.
    """


class GpuOomError(Exception):
    """Raised when a load/run hits CUDA out-of-memory; servicers
    translate this to RESOURCE_EXHAUSTED gpu_oom (matching the Go
    pipeline's ClassGPUOOM retry policy).
    """


class ModelManager:
    """Tracks which single engine is currently resident and serializes
    load/unload/run against it.
    """

    def __init__(self, registry: EngineRegistry) -> None:
        self._registry = registry
        self._lock = asyncio.Lock()
        self._resident_name: str | None = None
        self._resident_vram_mb = 0

    @property
    def resident_name(self) -> str | None:
        return self._resident_name

    @property
    def registry(self) -> EngineRegistry:
        return self._registry

    async def load(self, name: str) -> int:
        """Loads engine `name`, unloading whatever was resident first.
        Raises EngineNotInstalledError for an unknown/uninstalled engine,
        GpuOomError on a CUDA OOM during load.
        """
        async with self._lock:
            return await self._load_locked(name)

    async def _load_locked(self, name: str) -> int:
        """Assumes self._lock is already held by the caller."""
        engine = self._registry.get(name)
        if engine is None or not engine.installed():
            raise EngineNotInstalledError(name)
        if self._resident_name and self._resident_name != name:
            await self._unload_locked()
        try:
            held_mb = await engine.load()
        except Exception as exc:  # noqa: BLE001 - narrowed by is_cuda_oom below
            if _is_cuda_oom(exc):
                raise GpuOomError(str(exc)) from exc
            raise
        self._resident_name = name
        self._resident_vram_mb = held_mb
        return held_mb

    async def unload(self, name: str | None = None) -> None:
        """Unloads the resident engine if it matches name (or
        unconditionally if name is None, used by UnloadModel())."""
        async with self._lock:
            if name is not None and self._resident_name != name:
                return
            await self._unload_locked()

    async def _unload_locked(self) -> None:
        if self._resident_name is None:
            return
        engine = self._registry.get(self._resident_name)
        if engine is not None:
            await engine.unload()
        self._resident_name = None
        self._resident_vram_mb = 0

    async def run(self, name: str, request: object) -> object:
        """Runs request against engine `name`, auto-loading it first if
        it is not already resident. Holds the lock for the whole
        load-then-run sequence (not just the load): otherwise a
        concurrent load(B) could unload engine A mid-run, contradicting
        the "one resident engine at a time" invariant this class exists
        to enforce.
        """
        async with self._lock:
            if self._resident_name != name:
                await self._load_locked(name)
            engine = self._registry.get(name)
            if engine is None:
                raise EngineNotInstalledError(name)
            try:
                return await engine.run(request)
            except Exception as exc:  # noqa: BLE001 - narrowed by is_cuda_oom below
                if _is_cuda_oom(exc):
                    raise GpuOomError(str(exc)) from exc
                raise


def _is_cuda_oom(exc: Exception) -> bool:
    """CUDA OOM surfaces as a RuntimeError with "out of memory" in the
    message across the PyTorch/transformers stack; this string match is
    the same technique used across the ecosystem (there is no typed
    exception for it).
    """
    return "out of memory" in str(exc).lower()


async def abort_engine_not_installed(context: grpc.aio.ServicerContext, name: str) -> None:
    await context.abort(grpc.StatusCode.FAILED_PRECONDITION, f"engine_not_installed: {name}")


async def abort_gpu_oom(context: grpc.aio.ServicerContext, detail: str) -> None:
    await context.abort(grpc.StatusCode.RESOURCE_EXHAUSTED, f"gpu_oom: {detail}")
