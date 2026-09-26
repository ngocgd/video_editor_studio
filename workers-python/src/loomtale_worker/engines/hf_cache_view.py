"""An offline Hugging Face cache view over files on the models volume.
Some libraries fetch a dependency by repo id with hf_hub_download and
offer no local-path override (the VieNeu SDK does this for its audio
codec). With HF_HUB_OFFLINE=1 that call resolves refs/main and returns
the file from the cache's snapshot folder, so linking the pinned,
already-verified files into that layout satisfies it without any
network access and without copying gigabytes.
"""

from __future__ import annotations

from pathlib import Path


def build_view(cache_root: Path, repo: str, revision: str, files: dict[str, Path]) -> Path:
    """Links files (repo-relative name -> local path) into cache_root's
    layout for repo at revision and points refs/main at it. Returns the
    snapshot folder. Safe to call again: links are replaced."""
    repo_dir = cache_root / ("models--" + repo.replace("/", "--"))
    snapshot = repo_dir / "snapshots" / revision
    snapshot.mkdir(parents=True, exist_ok=True)
    refs = repo_dir / "refs"
    refs.mkdir(parents=True, exist_ok=True)
    (refs / "main").write_text(revision, encoding="utf-8")
    for name, target in files.items():
        link = snapshot / name
        link.parent.mkdir(parents=True, exist_ok=True)
        if link.is_symlink() or link.exists():
            link.unlink()
        link.symlink_to(target)
    return snapshot
