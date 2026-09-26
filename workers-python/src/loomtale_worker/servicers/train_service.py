"""Implements train.proto's Train streaming RPC.

The request's dataset_get_url is a presigned GET of a zip of the
character's approved refs (optional same-named .txt captions); the
trained LoRA (one .safetensors file) is PUT to output_put_url. The
stream carries TrainProgress (step counts and an ETA), TrainLog (the
trainer's console, scrubbed of URLs and credentials) and one final
TrainResult.

Params: steps, rank, learning_rate, trigger_word, max_minutes (see
engines/train_config.py) and output_key, echoed in the result.

The engine is made resident before the dataset is downloaded, so a
worker without the train runtime fails fast as engine_not_installed. If
the caller goes away mid-run the trainer process is killed, so the GPU
slot is not held for an abandoned job.
"""

from __future__ import annotations

import asyncio
import tempfile
import threading
import time
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any

from loomtale.worker.v1 import train_pb2, train_pb2_grpc
from loomtale_worker.engines.jobs import TrainJob, TrainOutput
from loomtale_worker.engines.threaded import InvalidJobError
from loomtale_worker.engines.train_config import train_params
from loomtale_worker.engines.train_dataset import extract_dataset
from loomtale_worker.model_manager import ModelManager
from loomtale_worker.servicers.streaming import (
    abort_invalid,
    fetch_input,
    finish_job,
    preload,
    push_output,
)

# 24 approved refs of a few MB each, with ample headroom.
MAX_DATASET_BYTES = 512 * 1024 * 1024


class TrainEventRelay:
    """Thread-safe sink for the engine's step and log callbacks; events()
    yields TrainEvents on the event loop until the job task finishes."""

    def __init__(self) -> None:
        self._loop = asyncio.get_running_loop()
        self._queue: asyncio.Queue[train_pb2.TrainEvent] = asyncio.Queue()
        self._started = time.monotonic()
        self._last_step = -1

    def step(self, step: int, total: int) -> None:
        self._loop.call_soon_threadsafe(self._on_step, step, total)

    def log(self, line: str) -> None:
        event = train_pb2.TrainEvent(log=train_pb2.TrainLog(line=line))
        self._loop.call_soon_threadsafe(self._queue.put_nowait, event)

    def _on_step(self, step: int, total: int) -> None:
        if step <= self._last_step or total <= 0:
            return
        self._last_step = step
        elapsed = time.monotonic() - self._started
        eta = int(elapsed * (total - step) / step) if step > 0 else 0
        progress = train_pb2.TrainProgress(
            pct=min(100, step * 100 // total), eta_s=eta, step=step, total_steps=total
        )
        self._queue.put_nowait(train_pb2.TrainEvent(progress=progress))

    async def events(self, task: asyncio.Task[Any]) -> AsyncIterator[train_pb2.TrainEvent]:
        while True:
            getter = asyncio.ensure_future(self._queue.get())
            done, _ = await asyncio.wait({getter, task}, return_when=asyncio.FIRST_COMPLETED)
            if getter not in done:
                getter.cancel()
                break
            yield getter.result()
        # Callbacks scheduled just before the task finished.
        await asyncio.sleep(0)
        while not self._queue.empty():
            yield self._queue.get_nowait()


class TrainServicer(train_pb2_grpc.TrainServicer):
    def __init__(self, manager: ModelManager) -> None:
        self._manager = manager

    async def Train(self, request, context):  # noqa: N802
        params = dict(request.params)
        if not request.dataset_get_url:
            await abort_invalid(context, "dataset_get_url is required")
        if not request.output_put_url:
            await abort_invalid(context, "output_put_url is required")
        try:
            train_params(params)
        except InvalidJobError as exc:
            await abort_invalid(context, str(exc))

        await preload(context, self._manager, request.engine)
        cancelled = threading.Event()
        with tempfile.TemporaryDirectory(prefix="train-") as tmp:
            work = Path(tmp)
            archive = work / "dataset.zip"
            archive.write_bytes(
                await fetch_input(context, request.dataset_get_url, MAX_DATASET_BYTES)
            )
            try:
                images = extract_dataset(archive, work / "dataset")
            except InvalidJobError as exc:
                await abort_invalid(context, str(exc))
            archive.unlink()

            relay = TrainEventRelay()
            job = TrainJob(
                dataset_dir=work / "dataset",
                work_dir=work,
                base_model=request.base_model,
                params=params,
                step=relay.step,
                log=relay.log,
                cancelled=cancelled.is_set,
            )
            task = asyncio.ensure_future(self._manager.run(request.engine, job))
            try:
                async for event in relay.events(task):
                    yield event
                out: TrainOutput = await finish_job(context, request.engine, task)
                weights = out.weights.read_bytes()
            finally:
                # The caller cancelled or the stream failed: stop the
                # trainer, then wait for it so the temp dir outlives it.
                if not task.done():
                    cancelled.set()
                    await asyncio.gather(task, return_exceptions=True)

        await push_output(context, request.output_put_url, weights, "application/octet-stream")
        yield train_pb2.TrainEvent(
            result=train_pb2.TrainResult(
                output_key=params.get("output_key", ""),
                metadata={**out.metadata, "engine": request.engine, "images": str(images)},
            )
        )


def register(server, manager: ModelManager) -> None:
    train_pb2_grpc.add_TrainServicer_to_server(TrainServicer(manager), server)
