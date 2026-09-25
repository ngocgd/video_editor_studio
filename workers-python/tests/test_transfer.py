"""Unit tests for the presigned-URL transfer helpers, exercised against
httpx.MockTransport so no real network is used.
"""

from __future__ import annotations

import httpx
import pytest

from loomtale_worker import transfer


def mock_client(handler) -> httpx.AsyncClient:
    return httpx.AsyncClient(transport=httpx.MockTransport(handler))


async def test_download_returns_bytes_under_cap():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, content=b"hello world")

    async with mock_client(handler) as client:
        data = await transfer.download(
            "https://example.internal/asset", max_bytes=1024, client=client
        )
    assert data == b"hello world"


async def test_download_rejects_content_length_over_cap():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, headers={"content-length": "999999999"}, content=b"x")

    async with mock_client(handler) as client:
        with pytest.raises(transfer.TransferTooLargeError):
            await transfer.download("https://example.internal/asset", max_bytes=10, client=client)


async def test_download_rejects_streamed_body_over_cap():
    def handler(request: httpx.Request) -> httpx.Response:
        # No content-length header, so the cap must be enforced while streaming.
        return httpx.Response(200, content=b"x" * 100)

    async with mock_client(handler) as client:
        with pytest.raises(transfer.TransferTooLargeError):
            await transfer.download("https://example.internal/asset", max_bytes=10, client=client)


async def test_upload_puts_data():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["method"] = request.method
        seen["content"] = request.content
        return httpx.Response(200)

    async with mock_client(handler) as client:
        await transfer.upload("https://example.internal/asset", b"payload", client=client)
    assert seen["method"] == "PUT"
    assert seen["content"] == b"payload"
