"""Helpers shared by the streaming media servicers: run an engine job on
the ModelManager while relaying its progress callbacks (made from the
engine's worker thread) as stream events, and map engine failures onto
gRPC status codes the Go side classifies (see workerconn.TranslateErr).
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import AsyncIterator, Awaitable
from typing import Any

import grpc
import httpx

from loomtale_worker import transfer
from loomtale_worker.engines.threaded import InvalidJobError
from loomtale_worker.model_manager import (
    EngineNotInstalledError,
    GpuOomError,
    ModelManager,
    abort_engine_not_installed,
    abort_gpu_oom,
)


class ProgressRelay:
    """A thread-safe progress sink for one job: engines call it from
    their worker thread; events() yields (pct, eta_s) on the event loop."""

    def __init__(self) -> None:
        self._loop = asyncio.get_running_loop()
        self._queue: asyncio.Queue[int] = asyncio.Queue()
        self._started = time.monotonic()
        self._last = -1

    def __call__(self, pct: int) -> None:
        self._loop.call_soon_threadsafe(self._queue.put_nowait, max(0, min(100, int(pct))))

    def _eta(self, pct: int) -> int:
        if pct <= 0:
            return 0
        elapsed = time.monotonic() - self._started
        return int(elapsed * (100 - pct) / pct)

    async def events(self, task: asyncio.Task[Any]) -> AsyncIterator[tuple[int, int]]:
        """Yields progress until task finishes, skipping repeats."""
        while True:
            getter = asyncio.ensure_future(self._queue.get())
            done, _ = await asyncio.wait({getter, task}, return_when=asyncio.FIRST_COMPLETED)
            if getter not in done:
                getter.cancel()
                break
            pct = getter.result()
            if pct > self._last:
                self._last = pct
                yield pct, self._eta(pct)
        while not self._queue.empty():
            pct = self._queue.get_nowait()
            if pct > self._last:
                self._last = pct
                yield pct, self._eta(pct)


async def abort_invalid(context: grpc.aio.ServicerContext, detail: str) -> None:
    await context.abort(grpc.StatusCode.INVALID_ARGUMENT, detail)


async def finish_job(
    context: grpc.aio.ServicerContext,
    engine: str,
    task: Awaitable[Any],
) -> Any:  # noqa: ANN401 - the engine's own output type
    """Awaits an engine job, aborting the RPC with the status matching
    its failure. Returns the job's output on success."""
    try:
        return await task
    except EngineNotInstalledError as exc:
        await abort_engine_not_installed(context, str(exc) or engine)
    except GpuOomError as exc:
        await abort_gpu_oom(context, str(exc))
    except InvalidJobError as exc:
        await abort_invalid(context, str(exc))
    except Exception as exc:  # noqa: BLE001 - surfaced to the caller as INTERNAL
        await context.abort(grpc.StatusCode.INTERNAL, f"{engine} failed: {exc}")


async def fetch_input(context: grpc.aio.ServicerContext, url: str, max_bytes: int) -> bytes:
    """Downloads a job input from its presigned URL. A rejected URL (4xx,
    e.g. expired or wrong) or an oversized body is the caller's error
    (INVALID_ARGUMENT, not retried); a network failure or 5xx is
    UNAVAILABLE, which the pipeline retries."""
    try:
        return await transfer.download(url, max_bytes=max_bytes)
    except transfer.TransferTooLargeError as exc:
        await abort_invalid(context, str(exc))
    except httpx.HTTPStatusError as exc:
        code = exc.response.status_code
        status = (
            grpc.StatusCode.INVALID_ARGUMENT if 400 <= code < 500 else grpc.StatusCode.UNAVAILABLE
        )
        await context.abort(status, f"input download failed with HTTP {code}")
    except httpx.HTTPError as exc:
        await context.abort(
            grpc.StatusCode.UNAVAILABLE, f"input download failed: {type(exc).__name__}"
        )
    return b""  # unreachable: every except branch aborts


async def push_output(
    context: grpc.aio.ServicerContext, url: str, data: bytes, content_type: str
) -> None:
    """Uploads a job output to its presigned URL, with the same status
    mapping as fetch_input."""
    try:
        await transfer.upload(url, data, content_type=content_type)
    except httpx.HTTPStatusError as exc:
        code = exc.response.status_code
        status = (
            grpc.StatusCode.INVALID_ARGUMENT if 400 <= code < 500 else grpc.StatusCode.UNAVAILABLE
        )
        await context.abort(status, f"output upload failed with HTTP {code}")
    except httpx.HTTPError as exc:
        await context.abort(
            grpc.StatusCode.UNAVAILABLE, f"output upload failed: {type(exc).__name__}"
        )


async def preload(context: grpc.aio.ServicerContext, manager: ModelManager, engine: str) -> None:
    """Makes engine resident before any input is fetched, so a missing
    engine or runtime fails fast as engine_not_installed instead of after
    downloading audio it could never process."""
    await finish_job(context, engine, manager.load(engine))
