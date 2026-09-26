"""Unpacks a character's training dataset archive (a zip of approved
reference images with optional same-named .txt captions) into a flat
directory. The archive is untrusted input: entries are flattened to
their base names (no path from the archive ever reaches the
filesystem), only image and caption files are kept, and entry count,
per-file size and total size are capped before anything is written.
"""

from __future__ import annotations

import zipfile
from pathlib import Path

from loomtale_worker.engines.threaded import InvalidJobError

IMAGE_SUFFIXES = frozenset({".png", ".jpg", ".jpeg", ".webp"})
CAPTION_SUFFIX = ".txt"
# A character keeps 20-24 approved refs; the smoke benchmark uses 4.
MIN_IMAGES = 4
MAX_IMAGES = 64
MAX_ENTRIES = 256
MAX_FILE_BYTES = 40 * 1024 * 1024
MAX_CAPTION_BYTES = 4 * 1024
MAX_TOTAL_BYTES = 1024 * 1024 * 1024


def extract_dataset(archive: Path, dest: Path) -> int:
    """Extracts archive into dest and returns the number of images.
    Raises InvalidJobError for anything that is not a usable dataset."""
    try:
        zf = zipfile.ZipFile(archive)
    except zipfile.BadZipFile as exc:
        raise InvalidJobError("the dataset archive is not a zip file") from exc
    with zf:
        infos = [i for i in zf.infolist() if not i.is_dir()]
        if len(infos) > MAX_ENTRIES:
            raise InvalidJobError(f"the dataset archive has more than {MAX_ENTRIES} entries")
        chosen: dict[str, zipfile.ZipInfo] = {}
        seen: set[str] = set()
        for info in infos:
            name = Path(info.filename.replace("\\", "/")).name
            suffix = Path(name).suffix.lower()
            if not name or name.startswith(".") or name.startswith("_"):
                continue
            if suffix not in IMAGE_SUFFIXES and suffix != CAPTION_SUFFIX:
                continue
            if name.lower() in seen:
                raise InvalidJobError(f"the dataset archive has two entries named {name!r}")
            seen.add(name.lower())
            cap = MAX_CAPTION_BYTES if suffix == CAPTION_SUFFIX else MAX_FILE_BYTES
            if info.file_size > cap:
                raise InvalidJobError(f"{name!r} is larger than {cap} bytes")
            chosen[name] = info

        images = [n for n in chosen if Path(n).suffix.lower() in IMAGE_SUFFIXES]
        if not MIN_IMAGES <= len(images) <= MAX_IMAGES:
            raise InvalidJobError(
                f"a training dataset needs {MIN_IMAGES} to {MAX_IMAGES} images, got {len(images)}"
            )
        if sum(i.file_size for i in chosen.values()) > MAX_TOTAL_BYTES:
            raise InvalidJobError(f"the dataset is larger than {MAX_TOTAL_BYTES} bytes")

        dest.mkdir(parents=True, exist_ok=True)
        for name, info in chosen.items():
            cap = info.file_size
            with zf.open(info) as src:
                # file_size comes from the archive header; read one byte
                # past it so a lying header is caught, not trusted.
                data = src.read(cap + 1)
            if len(data) > cap:
                raise InvalidJobError(f"{name!r} is larger than its archive header says")
            (dest / name).write_bytes(data)
    return len(images)
