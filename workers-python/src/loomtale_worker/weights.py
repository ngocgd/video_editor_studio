"""Weight-loading safety guard reused by every real engine (phases
9a-9c): only safetensors/GGUF files may be loaded, and PyTorch's
pickle-based torch.load path is never used, since an untrusted or
tampered checkpoint file can execute arbitrary code through pickle.
"""

from __future__ import annotations

from pathlib import Path

ALLOWED_WEIGHT_SUFFIXES = (".safetensors", ".gguf")


class UnsafeWeightsFormatError(Exception):
    """Raised when a weights path is not safetensors/GGUF."""


def assert_safe_weights_path(path: str | Path) -> Path:
    """Validates that path has an allowed suffix, returning it as a
    Path. Callers pass the result straight to safetensors.torch.load_file
    or a GGUF-aware loader, never torch.load (which defaults to
    pickle-based deserialization).
    """
    p = Path(path)
    if p.suffix.lower() not in ALLOWED_WEIGHT_SUFFIXES:
        raise UnsafeWeightsFormatError(
            f"{path}: only {ALLOWED_WEIGHT_SUFFIXES} weights are allowed, "
            "refusing to load a pickle-capable format"
        )
    return p
