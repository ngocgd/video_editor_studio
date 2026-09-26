"""Image decoding shared by the vision engines. Pillow comes with the
vision extra and is imported lazily, like every engine runtime.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from loomtale_worker.engines.threaded import InvalidJobError

# Scene renders and refs are at most a few megapixels; anything far larger
# is a caller bug or a decompression bomb.
MAX_PIXELS = 40_000_000


def load_rgb(path: Path) -> Any:  # noqa: ANN401 - a PIL.Image.Image
    """Opens an image as RGB, refusing undecodable or oversized files as
    an invalid job (not retried)."""
    from PIL import Image, UnidentifiedImageError

    try:
        with Image.open(path) as img:
            width, height = img.size
            if width * height > MAX_PIXELS:
                raise InvalidJobError(f"{path.name}: {width}x{height} exceeds {MAX_PIXELS} pixels")
            return img.convert("RGB")
    except (UnidentifiedImageError, OSError, Image.DecompressionBombError) as exc:
        raise InvalidJobError(f"{path.name} is not a readable image: {exc}") from exc
