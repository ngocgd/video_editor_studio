"""Tests for the vision engines without PyTorch or weights: the score and
depth math, the PNG encoder, and the Vision servicer against fake
engines (validation, downloads, uploads and status codes)."""

from __future__ import annotations

import struct
import zlib

import grpc
import numpy as np
import pytest

from loomtale_worker import transfer
from loomtale_worker.engines.jobs import DepthJob, DepthOutput, ScoreJob, ScoreOutput
from loomtale_worker.engines.registry import EngineRegistry
from loomtale_worker.engines.threaded import InvalidJobError
from loomtale_worker.engines.vision_math import (
    depth_to_uint16,
    gray16_png,
    similarity_to_references,
)
from loomtale_worker.model_manager import ModelManager
from loomtale_worker.servicers.vision_service import VisionServicer


class AbortSignal(Exception):
    def __init__(self, code, details):
        super().__init__(details)
        self.code = code
        self.details = details


class FakeContext:
    async def abort(self, code, details):
        raise AbortSignal(code, details)


class Request:
    def __init__(self, **kw):
        self.__dict__.update(kw)


class FakeVision:
    task = "vision"
    license = "Apache-2.0"

    def __init__(self, name: str, fail: Exception | None = None):
        self.name = name
        self.fail = fail
        self.jobs: list = []

    def installed(self) -> bool:
        return True

    async def load(self) -> int:
        return 0

    async def unload(self) -> None:
        return None

    async def run(self, job):
        self.jobs.append(job)
        if self.fail:
            raise self.fail
        if isinstance(job, ScoreJob):
            assert job.image_path.read_bytes() == b"image"
            return ScoreOutput(score=0.8125, metadata={"references": str(len(job.reference_paths))})
        assert isinstance(job, DepthJob)
        return DepthOutput(png=b"\x89PNG-depth", width=4, height=3)


def servicer(*engines) -> VisionServicer:
    registry = EngineRegistry()
    for e in engines:
        registry.register(e)
    return VisionServicer(ModelManager(registry))


@pytest.fixture
def storage(monkeypatch):
    stored: dict[str, tuple[bytes, str]] = {}
    fetched: list[str] = []

    async def fake_download(url, **_kw):
        fetched.append(url)
        return b"image" if url.endswith("/image") else b"ref"

    async def fake_upload(url, data, *, content_type="application/octet-stream", **_kw):
        stored[url] = (data, content_type)

    monkeypatch.setattr(transfer, "download", fake_download)
    monkeypatch.setattr(transfer, "upload", fake_upload)
    return stored, fetched


def score_request(engine="dinov2-base", refs=("http://minio/ref-a", "http://minio/ref-b")):
    return Request(
        engine=engine,
        image_get_url="http://minio/image",
        params={"reference_urls": "\n".join(refs)},
    )


def depth_request(engine="depth-anything-v2-small"):
    return Request(
        engine=engine,
        image_get_url="http://minio/image",
        output_put_url="http://minio/depth-out",
        params={"output_key": "t/scenes/1/depth.png"},
    )


def test_similarity_is_mean_min_max_cosine():
    image = np.array([1.0, 0.0])
    refs = np.array([[1.0, 0.0], [0.0, 1.0], [2.0, 0.0]])
    mean, lo, hi = similarity_to_references(image, refs)
    assert mean == pytest.approx(2 / 3)
    assert lo == pytest.approx(0.0)
    assert hi == pytest.approx(1.0)


def test_similarity_treats_zero_vectors_as_unrelated_and_rejects_bad_shapes():
    mean, lo, hi = similarity_to_references(np.zeros(3), np.ones((2, 3)))
    assert (mean, lo, hi) == (0.0, 0.0, 0.0)
    with pytest.raises(ValueError):
        similarity_to_references(np.ones(3), np.ones((0, 3)))
    with pytest.raises(ValueError):
        similarity_to_references(np.ones(3), np.ones((2, 4)))


def test_depth_is_normalised_to_the_full_uint16_range():
    out = depth_to_uint16(np.array([[0.5, 1.0], [2.5, np.nan]]))
    assert out.dtype == np.uint16
    assert out[0, 0] == 0 and out[1, 0] == 65535
    assert out[0, 1] == round(0.25 * 65535)
    assert out[1, 1] == 0
    assert not depth_to_uint16(np.full((2, 2), 3.0)).any()
    assert not depth_to_uint16(np.full((2, 2), np.inf)).any()


def decode_gray16_png(data: bytes) -> np.ndarray:
    assert data[:8] == b"\x89PNG\r\n\x1a\n"
    pos, idat, header = 8, b"", b""
    while pos < len(data):
        (length,) = struct.unpack(">I", data[pos : pos + 4])
        kind = data[pos + 4 : pos + 8]
        body = data[pos + 8 : pos + 8 + length]
        (crc,) = struct.unpack(">I", data[pos + 8 + length : pos + 12 + length])
        assert crc == zlib.crc32(kind + body) & 0xFFFFFFFF
        if kind == b"IHDR":
            header = body
        elif kind == b"IDAT":
            idat += body
        pos += 12 + length
    width, height, depth, color = struct.unpack(">IIBB", header[:10])
    assert (depth, color) == (16, 0)
    raw = zlib.decompress(idat)
    stride = 1 + width * 2
    rows = [raw[y * stride + 1 : (y + 1) * stride] for y in range(height)]
    assert all(raw[y * stride] == 0 for y in range(height))
    return np.frombuffer(b"".join(rows), dtype=">u2").reshape(height, width)


def test_gray16_png_round_trips():
    pixels = np.array([[0, 1, 65535], [256, 1000, 42]], dtype=np.uint16)
    assert np.array_equal(decode_gray16_png(gray16_png(pixels)), pixels)
    with pytest.raises(ValueError):
        gray16_png(pixels.astype(np.uint8))
    with pytest.raises(ValueError):
        gray16_png(np.zeros((0, 3), dtype=np.uint16))


async def test_score_downloads_image_and_refs_and_returns_the_score(storage):
    _, fetched = storage
    engine = FakeVision("dinov2-base")
    resp = await servicer(engine).Score(score_request(), FakeContext())
    assert resp.score == pytest.approx(0.8125)
    assert resp.metadata["references"] == "2"
    assert resp.metadata["engine"] == "dinov2-base"
    assert "score_s" in resp.metadata and "vram_peak_mb" in resp.metadata
    assert fetched == ["http://minio/image", "http://minio/ref-a", "http://minio/ref-b"]
    assert len(engine.jobs[0].reference_paths) == 2


@pytest.mark.parametrize(
    "request_",
    [
        score_request(refs=()),
        score_request(refs=tuple(f"http://minio/r{i}" for i in range(33))),
        Request(engine="dinov2-base", image_get_url="", params={"reference_urls": "http://a"}),
    ],
)
async def test_score_rejects_invalid_requests(storage, request_):
    with pytest.raises(AbortSignal) as exc:
        await servicer(FakeVision("dinov2-base")).Score(request_, FakeContext())
    assert exc.value.code == grpc.StatusCode.INVALID_ARGUMENT


async def test_unknown_engine_is_engine_not_installed_before_any_download(storage):
    _, fetched = storage
    with pytest.raises(AbortSignal) as exc:
        await servicer().Score(score_request(), FakeContext())
    assert exc.value.code == grpc.StatusCode.FAILED_PRECONDITION
    with pytest.raises(AbortSignal) as exc:
        await servicer().Depth(depth_request(), FakeContext())
    assert exc.value.code == grpc.StatusCode.FAILED_PRECONDITION
    assert fetched == []


async def test_engine_errors_map_to_status_codes(storage):
    engine = FakeVision("dinov2-base", fail=InvalidJobError("not an image"))
    with pytest.raises(AbortSignal) as exc:
        await servicer(engine).Score(score_request(), FakeContext())
    assert exc.value.code == grpc.StatusCode.INVALID_ARGUMENT
    engine = FakeVision("depth-anything-v2-small", fail=RuntimeError("boom"))
    with pytest.raises(AbortSignal) as exc:
        await servicer(engine).Depth(depth_request(), FakeContext())
    assert exc.value.code == grpc.StatusCode.INTERNAL


async def test_depth_uploads_png_and_echoes_the_output_key(storage):
    stored, _ = storage
    resp = await servicer(FakeVision("depth-anything-v2-small")).Depth(
        depth_request(), FakeContext()
    )
    assert resp.output_key == "t/scenes/1/depth.png"
    assert stored["http://minio/depth-out"] == (b"\x89PNG-depth", "image/png")


async def test_depth_requires_an_output_url(storage):
    req = depth_request()
    req.output_put_url = ""
    with pytest.raises(AbortSignal) as exc:
        await servicer(FakeVision("depth-anything-v2-small")).Depth(req, FakeContext())
    assert exc.value.code == grpc.StatusCode.INVALID_ARGUMENT
