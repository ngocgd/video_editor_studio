"""Implements align.proto's Align streaming RPC: aligns the narration
text to the audio at audio_get_url and uploads the subtitle cues as JSON
(see engines/jobs.py AlignOutput.to_json_dict) to output_put_url.

Request params: language ("en" or "vi", required) and output_key
(echoed back in the result).
"""

from __future__ import annotations

import asyncio
import json
import tempfile
import time
from pathlib import Path

from loomtale.worker.v1 import align_pb2, align_pb2_grpc
from loomtale_worker import transfer
from loomtale_worker.engines.jobs import AlignJob, AlignOutput
from loomtale_worker.model_manager import ModelManager
from loomtale_worker.servicers.streaming import ProgressRelay, abort_invalid, finish_job

LANGUAGES = ("en", "vi")
MAX_TEXT_CHARS = 200_000


class AlignServicer(align_pb2_grpc.AlignServicer):
    def __init__(self, manager: ModelManager) -> None:
        self._manager = manager

    async def Align(self, request, context):  # noqa: N802
        params = dict(request.params)
        language = params.get("language", "")
        text = request.text.strip()
        if language not in LANGUAGES:
            await abort_invalid(context, f"params.language must be one of {LANGUAGES}")
        if not text or len(text) > MAX_TEXT_CHARS:
            await abort_invalid(context, f"text must be 1 to {MAX_TEXT_CHARS} characters")
        if not request.audio_get_url or not request.output_put_url:
            await abort_invalid(context, "audio_get_url and output_put_url are required")

        with tempfile.TemporaryDirectory(prefix="align-") as tmp:
            audio = Path(tmp) / "narration.wav"
            audio.write_bytes(await transfer.download(request.audio_get_url))
            relay = ProgressRelay()
            job = AlignJob(audio_path=audio, text=text, language=language, progress=relay)
            started = time.monotonic()
            task = asyncio.ensure_future(self._manager.run(request.engine, job))
            async for pct, eta in relay.events(task):
                yield align_pb2.AlignEvent(progress=align_pb2.AlignProgress(pct=pct, eta_s=eta))
            out: AlignOutput = await finish_job(context, request.engine, task)
            elapsed = time.monotonic() - started

        body = json.dumps(out.to_json_dict(), ensure_ascii=False).encode("utf-8")
        await transfer.upload(request.output_put_url, body, content_type="application/json")
        yield align_pb2.AlignEvent(
            result=align_pb2.AlignResult(
                output_key=params.get("output_key", ""),
                segment_count=len(out.segments),
                metadata={
                    "engine": request.engine,
                    "language": language,
                    "granularity": out.granularity,
                    "audio_duration_s": f"{out.audio_duration_s:.3f}",
                    "matched_ratio": f"{out.matched_ratio:.4f}",
                    "align_s": f"{elapsed:.3f}",
                },
            )
        )


def register(server, manager: ModelManager) -> None:
    align_pb2_grpc.add_AlignServicer_to_server(AlignServicer(manager), server)
