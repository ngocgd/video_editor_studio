"""Common base for the real engines: model code is blocking (PyTorch,
CTranslate2, ONNX Runtime), so load/run/unload execute in a worker
thread and the gRPC event loop stays responsive. The heavy libraries are
imported only inside _load_sync, so this package (and its tests) import
cleanly on a worker image built without an engine's extra.
"""

from __future__ import annotations

import asyncio
import gc
from typing import Any

from loomtale_worker.engines.local_files import LocalFiles


class InvalidJobError(Exception):
    """A request the engine cannot run as given (wrong language, missing
    reference voice, out-of-range parameter); the servicer answers
    INVALID_ARGUMENT and the pipeline does not retry it."""


def float_param(params: dict[str, str], key: str, default: float, lo: float, hi: float) -> float:
    raw = params.get(key, "")
    if raw == "":
        return default
    try:
        value = float(raw)
    except ValueError as exc:
        raise InvalidJobError(f"{key} must be a number, got {raw!r}") from exc
    if not lo <= value <= hi:
        raise InvalidJobError(f"{key} must be between {lo} and {hi}, got {value}")
    return value


def int_param(params: dict[str, str], key: str, default: int, lo: int, hi: int) -> int:
    raw = params.get(key, "")
    if raw == "":
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise InvalidJobError(f"{key} must be an integer, got {raw!r}") from exc
    if not lo <= value <= hi:
        raise InvalidJobError(f"{key} must be between {lo} and {hi}, got {value}")
    return value


def cuda_held_mb() -> int:
    """VRAM held by this process's PyTorch allocator, 0 without torch or
    CUDA."""
    try:
        import torch
    except ImportError:
        return 0
    if not torch.cuda.is_available():
        return 0
    return int(torch.cuda.memory_reserved() // (1024 * 1024))


def cuda_peak_mb_and_reset() -> int:
    """Peak VRAM reserved since the last reset, then resets the counter."""
    try:
        import torch
    except ImportError:
        return 0
    if not torch.cuda.is_available():
        return 0
    peak = int(torch.cuda.max_memory_reserved() // (1024 * 1024))
    torch.cuda.reset_peak_memory_stats()
    return peak


def release_cuda() -> None:
    gc.collect()
    try:
        import torch
    except ImportError:
        return
    if torch.cuda.is_available():
        torch.cuda.empty_cache()


class ThreadedEngine:
    """Subclasses set name/task/license/files and implement _load_sync,
    _unload_sync and _run_sync."""

    name: str
    task: str
    license: str
    files: LocalFiles

    def installed(self) -> bool:
        return self.files.present()

    async def load(self) -> int:
        self.files.assert_safe()
        await asyncio.to_thread(self._load_sync)
        return cuda_held_mb()

    async def unload(self) -> None:
        await asyncio.to_thread(self._unload_sync)
        release_cuda()

    async def run(self, request: Any) -> Any:  # noqa: ANN401 - per-task job/output types
        return await asyncio.to_thread(self._run_sync, request)

    def _load_sync(self) -> None:
        raise NotImplementedError

    def _unload_sync(self) -> None:
        raise NotImplementedError

    def _run_sync(self, request: Any) -> Any:  # noqa: ANN401
        raise NotImplementedError
