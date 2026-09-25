"""Presigned-URL transfer helpers. The Python worker never holds S3
credentials (per the contract): every input/output is an internal
presigned URL from the Go side's storage.Internal, fetched/pushed here
over plain HTTPS with a hard size cap so a malformed or malicious URL can
never exhaust this process's memory.
"""

from __future__ import annotations

import httpx

# 512MB covers the largest media asset this worker ever handles directly
# (audio/small video clips); anything larger indicates a caller error.
MAX_TRANSFER_BYTES = 512 * 1024 * 1024

DEFAULT_TIMEOUT_S = 300.0


class TransferTooLargeError(Exception):
    """Raised when a download would exceed MAX_TRANSFER_BYTES."""


async def download(
    url: str, *, max_bytes: int = MAX_TRANSFER_BYTES, client: httpx.AsyncClient | None = None
) -> bytes:
    """Streams url into memory up to max_bytes, raising
    TransferTooLargeError if the response is (or claims to be) larger.
    client is injectable for tests (see tests/test_transfer.py); normal
    callers omit it and get a fresh short-lived client per call.
    """
    owns_client = client is None
    client = client or httpx.AsyncClient(timeout=DEFAULT_TIMEOUT_S)
    try:
        async with client.stream("GET", url) as resp:
            resp.raise_for_status()
            content_length = resp.headers.get("content-length")
            if content_length is not None and int(content_length) > max_bytes:
                raise TransferTooLargeError(
                    f"{url}: content-length {content_length} exceeds cap {max_bytes}"
                )

            chunks = bytearray()
            async for chunk in resp.aiter_bytes():
                chunks.extend(chunk)
                if len(chunks) > max_bytes:
                    raise TransferTooLargeError(f"{url}: exceeded cap {max_bytes} while streaming")
            return bytes(chunks)
    finally:
        if owns_client:
            await client.aclose()


async def upload(
    url: str,
    data: bytes,
    *,
    content_type: str = "application/octet-stream",
    client: httpx.AsyncClient | None = None,
) -> None:
    """PUTs data to a presigned URL. client is injectable for tests."""
    owns_client = client is None
    client = client or httpx.AsyncClient(timeout=DEFAULT_TIMEOUT_S)
    try:
        resp = await client.put(url, content=data, headers={"Content-Type": content_type})
        resp.raise_for_status()
    finally:
        if owns_client:
            await client.aclose()
