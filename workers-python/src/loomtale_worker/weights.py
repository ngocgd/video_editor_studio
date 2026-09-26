"""Weight-loading safety guard reused by every real engine: only formats
that cannot execute code on load may be read, and PyTorch's pickle-based
torch.load path is never used for model weights, since an untrusted or
tampered checkpoint file can execute arbitrary code through pickle.

Allowed: safetensors, GGUF, ONNX graphs and their external data, numpy
.npz archives (loaded with pickles disabled), small config files, and a
CTranslate2 model.bin (a flat tensor file) only where the caller says the
file is one. models/manifest.yaml is linted against the same rule.
"""

from __future__ import annotations

from pathlib import Path

ALLOWED_WEIGHT_SUFFIXES = (".safetensors", ".gguf", ".onnx", ".data", ".npz")
ALLOWED_CONFIG_SUFFIXES = (".json", ".txt", ".yaml")


class UnsafeWeightsFormatError(Exception):
    """Raised when a weights path is in a pickle-capable format."""


def assert_safe_weights_path(path: str | Path, *, ctranslate2: bool = False) -> Path:
    """Validates that path has an allowed suffix, returning it as a Path.
    ctranslate2=True additionally allows a .bin file, for the one caller
    that loads it through CTranslate2's own reader. Callers never pass a
    weights path to torch.load.
    """
    p = Path(path)
    suffix = p.suffix.lower()
    if suffix in ALLOWED_WEIGHT_SUFFIXES or suffix in ALLOWED_CONFIG_SUFFIXES:
        return p
    if ctranslate2 and suffix == ".bin":
        return p
    raise UnsafeWeightsFormatError(
        f"{path}: only {ALLOWED_WEIGHT_SUFFIXES} weights are allowed, "
        "refusing to load a pickle-capable format"
    )
