"""Engine weight directories on the read-only models volume. Every engine
names the files it needs relative to the volume root, exactly as
models/manifest.yaml pins them (a test cross-checks the two), and counts
as installed only when all of them are present. The Go side has already
verified each file's digest before it asks this worker to load.
"""

from __future__ import annotations

import os
from pathlib import Path

from loomtale_worker.weights import assert_safe_weights_path

DEFAULT_MODELS_DIR = "/models"


def models_dir() -> Path:
    return Path(os.environ.get("MODELS_DIR", DEFAULT_MODELS_DIR))


class LocalFiles:
    """The files of one engine under root. ctranslate2 names the files
    (relative paths) that are CTranslate2 model files."""

    def __init__(
        self, root: Path, relative_paths: tuple[str, ...], ctranslate2: tuple[str, ...] = ()
    ) -> None:
        self.root = root
        self.relative_paths = relative_paths
        self.ctranslate2 = ctranslate2

    def path(self, relative: str) -> Path:
        if relative not in self.relative_paths:
            raise KeyError(f"{relative} is not one of this engine's pinned files")
        return self.root / relative

    def present(self) -> bool:
        return all((self.root / p).is_file() for p in self.relative_paths)

    def assert_safe(self) -> None:
        """Refuses to load if any file is in a pickle-capable format (the
        manifest linter already refuses them; this is the load-time half
        of the same rule)."""
        for p in self.relative_paths:
            assert_safe_weights_path(self.root / p, ctranslate2=p in self.ctranslate2)
