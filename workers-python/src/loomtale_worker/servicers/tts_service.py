"""Implements tts.proto's Synthesize streaming RPC.

Request params (all optional unless noted):
- language: "en" or "vi" (required);
- reference_url: presigned GET of the voice preset's reference audio to
  clone; only accepted together with consent=granted, which the Go voice
  step sets from the preset's recorded consent flag;
- output_key: echoed back in the result (the storage key the Go side
  presigned output_put_url for);
- engine-specific tuning (temperature, exaggeration, cfg_weight, seed).

The audio is uploaded to output_put_url as 16-bit PCM mono WAV.
"""

from __future__ import annotations

import asyncio
import tempfile
import time
from pathlib import Path

import grpc

from loomtale.worker.v1 import tts_pb2, tts_pb2_grpc
from loomtale_worker import transfer
from loomtale_worker.engines.audio import to_wav_bytes
from loomtale_worker.engines.jobs import SynthesisJob, SynthesisOutput
from loomtale_worker.engines.text_chunks import count_words
from loomtale_worker.engines.threaded import cuda_peak_mb_and_reset
from loomtale_worker.model_manager import ModelManager
from loomtale_worker.servicers.streaming import ProgressRelay, abort_invalid, finish_job

LANGUAGES = ("en", "vi")
# About three hours of narration; a longer request is a caller bug.
MAX_TEXT_CHARS = 200_000
# A reference clip is a few seconds of speech.
MAX_REFERENCE_BYTES = 50 * 1024 * 1024
CONSENT_GRANTED = "granted"


class TTSServicer(tts_pb2_grpc.TTSServicer):
    def __init__(self, manager: ModelManager) -> None:
        self._manager = manager

    async def Synthesize(self, request, context):  # noqa: N802
        params = dict(request.params)
        language = params.get("language", "")
        text = request.text.strip()
        if language not in LANGUAGES:
            await abort_invalid(context, f"params.language must be one of {LANGUAGES}")
        if not text or len(text) > MAX_TEXT_CHARS:
            await abort_invalid(context, f"text must be 1 to {MAX_TEXT_CHARS} characters")
        if not request.output_put_url:
            await abort_invalid(context, "output_put_url is required")
        reference_url = params.get("reference_url", "")
        if reference_url and params.get("consent") != CONSENT_GRANTED:
            await context.abort(
                grpc.StatusCode.PERMISSION_DENIED,
                "voice_consent_required: cloning a reference voice needs the preset's consent flag",
            )

        with tempfile.TemporaryDirectory(prefix="tts-") as tmp:
            reference = None
            if reference_url:
                reference = Path(tmp) / "reference.wav"
                reference.write_bytes(
                    await transfer.download(reference_url, max_bytes=MAX_REFERENCE_BYTES)
                )
            relay = ProgressRelay()
            job = SynthesisJob(
                text=text,
                language=language,
                voice=request.voice,
                reference_wav=reference,
                params=params,
                progress=relay,
            )
            cuda_peak_mb_and_reset()
            started = time.monotonic()
            task = asyncio.ensure_future(self._manager.run(request.engine, job))
            async for pct, eta in relay.events(task):
                yield tts_pb2.SynthesizeEvent(
                    progress=tts_pb2.SynthesizeProgress(pct=pct, eta_s=eta)
                )
            out: SynthesisOutput = await finish_job(context, request.engine, task)
            elapsed = time.monotonic() - started
            vram_peak = cuda_peak_mb_and_reset()

        await transfer.upload(
            request.output_put_url,
            to_wav_bytes(out.samples, out.sample_rate),
            content_type="audio/wav",
        )
        duration = out.duration_s
        yield tts_pb2.SynthesizeEvent(
            result=tts_pb2.SynthesizeResult(
                output_key=params.get("output_key", ""),
                duration_s=duration,
                metadata={
                    "engine": request.engine,
                    "language": language,
                    "sample_rate": str(out.sample_rate),
                    "words": str(count_words(text)),
                    "synthesis_s": f"{elapsed:.3f}",
                    "rtf": f"{elapsed / duration:.4f}" if duration > 0 else "0",
                    "vram_peak_mb": str(vram_peak),
                },
            )
        )


def register(server, manager: ModelManager) -> None:
    tts_pb2_grpc.add_TTSServicer_to_server(TTSServicer(manager), server)
