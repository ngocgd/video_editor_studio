"""Model-free parts of the vision engines, kept apart so they are unit
tested without PyTorch or weights: turning embeddings into a consistency
score, and turning a depth prediction into a 16-bit grayscale PNG.
"""

from __future__ import annotations

import struct
import zlib

import numpy as np


def similarity_to_references(
    image: np.ndarray, references: np.ndarray
) -> tuple[float, float, float]:
    """Cosine similarity of one embedding (D,) to each reference embedding
    (N, D); returns (mean, min, max). A zero vector counts as similarity 0
    instead of dividing by zero."""
    image = np.asarray(image, dtype=np.float64).reshape(-1)
    references = np.asarray(references, dtype=np.float64)
    if references.ndim != 2 or references.shape[0] == 0:
        raise ValueError("references must be a non-empty (N, D) array")
    if references.shape[1] != image.shape[0]:
        raise ValueError("image and reference embeddings differ in dimension")
    denom = np.linalg.norm(references, axis=1) * np.linalg.norm(image)
    dots = references @ image
    sims = np.divide(dots, denom, out=np.zeros_like(dots), where=denom > 0)
    sims = np.clip(sims, -1.0, 1.0)
    return float(sims.mean()), float(sims.min()), float(sims.max())


def depth_to_uint16(depth: np.ndarray) -> np.ndarray:
    """Min-max normalises a (H, W) depth prediction to the full uint16
    range. A flat prediction, or one without finite values, becomes all
    zeros instead of dividing by zero."""
    depth = np.asarray(depth, dtype=np.float64)
    if depth.ndim != 2:
        raise ValueError("depth must be a (H, W) array")
    finite = np.isfinite(depth)
    if not finite.any():
        return np.zeros(depth.shape, dtype=np.uint16)
    lo = depth[finite].min()
    hi = depth[finite].max()
    if hi - lo <= 0:
        return np.zeros(depth.shape, dtype=np.uint16)
    scaled = (np.where(finite, depth, lo) - lo) / (hi - lo)
    return np.round(scaled * 65535.0).astype(np.uint16)


def _png_chunk(kind: bytes, data: bytes) -> bytes:
    body = kind + data
    return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)


def gray16_png(pixels: np.ndarray) -> bytes:
    """Encodes a (H, W) uint16 array as a 16-bit grayscale PNG without an
    image library."""
    pixels = np.asarray(pixels)
    if pixels.ndim != 2 or pixels.dtype != np.uint16:
        raise ValueError("pixels must be a (H, W) uint16 array")
    height, width = pixels.shape
    if height == 0 or width == 0:
        raise ValueError("pixels must not be empty")
    rows = pixels.astype(">u2").tobytes()
    stride = width * 2
    # Filter type 0 (none) before every scanline.
    raw = b"".join(b"\x00" + rows[y * stride : (y + 1) * stride] for y in range(height))
    header = struct.pack(">IIBBBBB", width, height, 16, 0, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + _png_chunk(b"IHDR", header)
        + _png_chunk(b"IDAT", zlib.compress(raw, 6))
        + _png_chunk(b"IEND", b"")
    )
