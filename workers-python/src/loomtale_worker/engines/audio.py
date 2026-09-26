"""WAV encoding for engine output and simple sample-level helpers. Engines
return float32 samples; everything leaving the worker is 16-bit PCM mono
WAV, which every later step (align, render) reads without a codec.
"""

from __future__ import annotations

import io
import wave

import numpy as np


def to_wav_bytes(samples: np.ndarray, sample_rate: int) -> bytes:
    """Encodes mono float samples in [-1, 1] as 16-bit PCM WAV."""
    clipped = np.clip(np.asarray(samples, dtype=np.float32).reshape(-1), -1.0, 1.0)
    pcm = (clipped * 32767.0).astype("<i2")
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(int(sample_rate))
        w.writeframes(pcm.tobytes())
    return buf.getvalue()


def read_wav(data: bytes) -> tuple[np.ndarray, int]:
    """Decodes 16-bit PCM WAV (mono, or the first channel of stereo) to
    float32 samples and the sample rate."""
    with wave.open(io.BytesIO(data), "rb") as w:
        if w.getsampwidth() != 2:
            raise ValueError("only 16-bit PCM WAV is supported")
        channels = w.getnchannels()
        rate = w.getframerate()
        raw = w.readframes(w.getnframes())
    pcm = np.frombuffer(raw, dtype="<i2")
    if channels > 1:
        pcm = pcm[::channels]
    return pcm.astype(np.float32) / 32768.0, rate


def join_with_pauses(chunks: list[np.ndarray], sample_rate: int, pause_s: float) -> np.ndarray:
    """Concatenates chunks with pause_s of silence between them."""
    if not chunks:
        return np.zeros(0, dtype=np.float32)
    gap = np.zeros(int(round(pause_s * sample_rate)), dtype=np.float32)
    parts: list[np.ndarray] = []
    for i, chunk in enumerate(chunks):
        if i:
            parts.append(gap)
        parts.append(np.asarray(chunk, dtype=np.float32).reshape(-1))
    return np.concatenate(parts)
