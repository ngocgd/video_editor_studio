"""Tests for the TTS and Align servicers against fake engines: request
validation, the voice-consent gate, progress relaying, uploads and the
status codes the Go side classifies."""

from __future__ import annotations

import json

import grpc
import httpx
import numpy as np
import pytest

from loomtale_worker import transfer
from loomtale_worker.engines.audio import read_wav
from loomtale_worker.engines.jobs import (
    AlignJob,
    AlignOutput,
    SynthesisJob,
    SynthesisOutput,
    TimedSegment,
    TimedWord,
)
from loomtale_worker.engines.registry import EngineRegistry
from loomtale_worker.engines.threaded import InvalidJobError
from loomtale_worker.model_manager import ModelManager
from loomtale_worker.servicers.align_service import AlignServicer
from loomtale_worker.servicers.tts_service import TTSServicer


class AbortSignal(Exception):
    def __init__(self, code, details):
        super().__init__(details)
        self.code = code
        self.details = details


class FakeContext:
    async def abort(self, code, details):
        raise AbortSignal(code, details)


class FakeTTS:
    name = "fake-tts"
    task = "tts"
    license = "MIT"

    def __init__(self, fail: Exception | None = None):
        self.jobs: list[SynthesisJob] = []
        self.fail = fail

    def installed(self) -> bool:
        return True

    async def load(self) -> int:
        return 0

    async def unload(self) -> None:
        return None

    async def run(self, job: SynthesisJob) -> SynthesisOutput:
        self.jobs.append(job)
        if self.fail:
            raise self.fail
        for pct in (25, 50, 50, 100):
            job.progress(pct)
        return SynthesisOutput(samples=np.zeros(24_000, dtype=np.float32), sample_rate=24_000)


class FakeAligner:
    name = "fake-align"
    task = "align"
    license = "MIT"

    def installed(self) -> bool:
        return True

    async def load(self) -> int:
        return 0

    async def unload(self) -> None:
        return None

    async def run(self, job: AlignJob) -> AlignOutput:
        assert job.audio_path.read_bytes() == b"RIFFaudio"
        job.progress(100)
        return AlignOutput(
            language=job.language,
            granularity="word",
            segments=[TimedSegment("Hi there.", 0.1, 0.9, [TimedWord("Hi", 0.1, 0.4)])],
            audio_duration_s=1.0,
            matched_ratio=1.0,
        )


class Request:
    def __init__(self, **kw):
        self.__dict__.update(kw)


@pytest.fixture
def uploads(monkeypatch):
    stored: dict[str, tuple[bytes, str]] = {}
    downloads = {"http://minio/ref": b"reference-bytes", "http://minio/audio": b"RIFFaudio"}

    async def fake_download(url, **_kw):
        return downloads[url]

    async def fake_upload(url, data, *, content_type="application/octet-stream", **_kw):
        stored[url] = (data, content_type)

    monkeypatch.setattr(transfer, "download", fake_download)
    monkeypatch.setattr(transfer, "upload", fake_upload)
    return stored


def tts_servicer(engine) -> TTSServicer:
    registry = EngineRegistry()
    registry.register(engine)
    return TTSServicer(ModelManager(registry))


def tts_request(**params):
    return Request(
        engine="fake-tts",
        voice="",
        text="Hello there. General Kenobi.",
        output_put_url="http://minio/out",
        params={"language": "en", "output_key": "k/voice.wav", **params},
    )


async def collect(stream):
    return [event async for event in stream]


async def test_synthesize_streams_progress_uploads_wav_and_reports_metadata(uploads):
    engine = FakeTTS()
    events = await collect(tts_servicer(engine).Synthesize(tts_request(), FakeContext()))
    progress = [e.progress.pct for e in events if e.HasField("progress")]
    assert progress == [25, 50, 100]
    result = events[-1].result
    assert result.output_key == "k/voice.wav"
    assert result.duration_s == pytest.approx(1.0)
    assert result.metadata["words"] == "4"
    assert result.metadata["sample_rate"] == "24000"
    data, content_type = uploads["http://minio/out"]
    assert content_type == "audio/wav"
    samples, rate = read_wav(data)
    assert rate == 24_000 and len(samples) == 24_000
    assert engine.jobs[0].reference_wav is None


async def test_cloning_needs_the_consent_flag(uploads):
    engine = FakeTTS()
    with pytest.raises(AbortSignal) as exc:
        await collect(
            tts_servicer(engine).Synthesize(
                tts_request(reference_url="http://minio/ref"), FakeContext()
            )
        )
    assert exc.value.code == grpc.StatusCode.PERMISSION_DENIED
    assert "voice_consent_required" in exc.value.details
    assert engine.jobs == [] and uploads == {}


async def test_cloning_with_consent_passes_the_reference_clip(uploads):
    seen: list[bytes] = []

    class Capturing(FakeTTS):
        async def run(self, job):
            seen.append(job.reference_wav.read_bytes())
            return await super().run(job)

    await collect(
        tts_servicer(Capturing()).Synthesize(
            tts_request(reference_url="http://minio/ref", consent="granted"), FakeContext()
        )
    )
    assert seen == [b"reference-bytes"]


@pytest.mark.parametrize(
    ("change", "fragment"),
    [
        ({"params": {"language": "fr"}}, "params.language"),
        ({"text": "   "}, "text must be"),
        ({"output_put_url": ""}, "output_put_url"),
    ],
)
async def test_synthesize_rejects_invalid_requests(uploads, change, fragment):
    req = tts_request()
    req.__dict__.update(change)
    with pytest.raises(AbortSignal) as exc:
        await collect(tts_servicer(FakeTTS()).Synthesize(req, FakeContext()))
    assert exc.value.code == grpc.StatusCode.INVALID_ARGUMENT
    assert fragment in exc.value.details


async def test_engine_errors_map_to_status_codes(uploads):
    cases = [
        (InvalidJobError("chatterbox narrates English only"), grpc.StatusCode.INVALID_ARGUMENT),
        (RuntimeError("CUDA out of memory"), grpc.StatusCode.RESOURCE_EXHAUSTED),
        (RuntimeError("boom"), grpc.StatusCode.INTERNAL),
    ]
    for error, code in cases:
        with pytest.raises(AbortSignal) as exc:
            await collect(
                tts_servicer(FakeTTS(fail=error)).Synthesize(tts_request(), FakeContext())
            )
        assert exc.value.code == code
    assert uploads == {}


async def test_unknown_engine_is_engine_not_installed(uploads):
    req = tts_request()
    req.engine = "nope"
    with pytest.raises(AbortSignal) as exc:
        await collect(tts_servicer(FakeTTS()).Synthesize(req, FakeContext()))
    assert exc.value.code == grpc.StatusCode.FAILED_PRECONDITION
    assert "engine_not_installed" in exc.value.details


async def test_align_uploads_cue_json(uploads):
    registry = EngineRegistry()
    registry.register(FakeAligner())
    servicer = AlignServicer(ModelManager(registry))
    req = Request(
        engine="fake-align",
        audio_get_url="http://minio/audio",
        text="Hi there.",
        output_put_url="http://minio/cues",
        params={"language": "en", "output_key": "k/cues.json"},
    )
    events = await collect(servicer.Align(req, FakeContext()))
    result = events[-1].result
    assert result.segment_count == 1
    assert result.output_key == "k/cues.json"
    assert result.metadata["granularity"] == "word"
    body, content_type = uploads["http://minio/cues"]
    assert content_type == "application/json"
    cues = json.loads(body)
    assert cues["segments"][0]["words"][0] == {"word": "Hi", "start": 0.1, "end": 0.4}


async def test_align_rejects_an_unsupported_language(uploads):
    servicer = AlignServicer(ModelManager(EngineRegistry()))
    req = Request(
        engine="fake-align",
        audio_get_url="http://minio/audio",
        text="Hi",
        output_put_url="http://minio/cues",
        params={"language": "de"},
    )
    with pytest.raises(AbortSignal) as exc:
        await collect(servicer.Align(req, FakeContext()))
    assert exc.value.code == grpc.StatusCode.INVALID_ARGUMENT


async def test_missing_engine_fails_before_any_input_is_downloaded(monkeypatch):
    fetched: list[str] = []

    async def fake_download(url, **_kw):
        fetched.append(url)
        return b""

    monkeypatch.setattr(transfer, "download", fake_download)
    req = tts_request(reference_url="http://minio/ref", consent="granted")
    req.engine = "nope"
    with pytest.raises(AbortSignal) as exc:
        await collect(tts_servicer(FakeTTS()).Synthesize(req, FakeContext()))
    assert exc.value.code == grpc.StatusCode.FAILED_PRECONDITION
    assert fetched == []


@pytest.mark.parametrize(
    ("status_code", "grpc_code"),
    [(403, grpc.StatusCode.INVALID_ARGUMENT), (503, grpc.StatusCode.UNAVAILABLE)],
)
async def test_input_download_failures_map_to_status_codes(monkeypatch, status_code, grpc_code):
    async def failing_download(url, **_kw):
        request = httpx.Request("GET", url)
        raise httpx.HTTPStatusError(
            "failed", request=request, response=httpx.Response(status_code, request=request)
        )

    monkeypatch.setattr(transfer, "download", failing_download)
    with pytest.raises(AbortSignal) as exc:
        await collect(
            tts_servicer(FakeTTS()).Synthesize(
                tts_request(reference_url="http://minio/ref", consent="granted"), FakeContext()
            )
        )
    assert exc.value.code == grpc_code
    assert str(status_code) in exc.value.details


async def test_output_upload_failure_is_unavailable(monkeypatch):
    async def failing_upload(url, data, **_kw):
        raise httpx.ConnectError("refused")

    monkeypatch.setattr(transfer, "upload", failing_upload)
    with pytest.raises(AbortSignal) as exc:
        await collect(tts_servicer(FakeTTS()).Synthesize(tts_request(), FakeContext()))
    assert exc.value.code == grpc.StatusCode.UNAVAILABLE
