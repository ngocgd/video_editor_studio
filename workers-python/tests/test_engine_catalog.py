"""Tests for the registered engines without their runtimes or weights:
file lists match the manifest, installed() follows the files on disk, a
missing runtime is an honest engine_not_installed, and the offline HF
cache view has the layout hf_hub_download reads."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest
import yaml

from loomtale_worker.engines import chatterbox, depth_small, dinov2_score, vieneu, whisper_align
from loomtale_worker.engines.catalog import build_registry
from loomtale_worker.engines.hf_cache_view import build_view
from loomtale_worker.model_manager import EngineNotInstalledError, ModelManager
from loomtale_worker.weights import UnsafeWeightsFormatError, assert_safe_weights_path

REPO_ROOT = Path(__file__).resolve().parents[2]
ENGINE_FILES = {
    "chatterbox": chatterbox.FILES,
    "vieneu-v3-turbo": vieneu.FILES,
    "whisper-align": whisper_align.FILES,
    "dinov2-base": dinov2_score.FILES,
    "depth-anything-v2-small": depth_small.FILES,
}


def manifest_entries() -> dict[str, dict]:
    raw = yaml.safe_load((REPO_ROOT / "models" / "manifest.yaml").read_text(encoding="utf-8"))
    return {e["name"]: e for e in raw["models"]}


def touch_all(root: Path, files: tuple[str, ...]) -> None:
    for rel in files:
        p = root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(b"x")


def test_engine_files_are_exactly_the_manifest_pins():
    entries = manifest_entries()
    for name, files in ENGINE_FILES.items():
        entry = entries[name]
        assert entry["engine"] == "pyworker"
        assert sorted(f["path"] for f in entry["files"]) == sorted(files), name


def test_ctranslate2_files_match_the_manifest_format():
    entry = manifest_entries()["whisper-align"]
    declared = sorted(f["path"] for f in entry["files"] if f.get("format") == "ctranslate2")
    assert declared == sorted(whisper_align.CTRANSLATE2_FILES)


def test_registry_lists_every_engine_not_installed_on_an_empty_volume(tmp_path):
    registry = build_registry(tmp_path)
    engines = {e.name: e for e in registry.list()}
    assert set(engines) == set(ENGINE_FILES)
    assert {e.task for e in engines.values()} == {"tts", "align", "vision"}
    assert not any(e.installed() for e in engines.values())


def test_engine_is_installed_only_when_every_file_is_present(tmp_path):
    engine = build_registry(tmp_path).get("whisper-align")
    touch_all(tmp_path, whisper_align.FILES[:-1])
    assert not engine.installed()
    touch_all(tmp_path, whisper_align.FILES[-1:])
    assert engine.installed()


async def test_installed_engine_without_its_runtime_is_engine_not_installed(tmp_path, monkeypatch):
    # Simulate an image built without the tts-en extra even where the
    # test environment happens to have it.
    monkeypatch.setitem(sys.modules, "chatterbox", None)
    monkeypatch.setitem(sys.modules, "chatterbox.tts", None)
    touch_all(tmp_path, chatterbox.FILES)
    manager = ModelManager(build_registry(tmp_path))
    with pytest.raises(EngineNotInstalledError, match="lacks the engine's runtime"):
        await manager.load("chatterbox")
    assert manager.resident_name is None


@pytest.mark.parametrize(
    ("engine", "files"),
    [("dinov2-base", dinov2_score.FILES), ("depth-anything-v2-small", depth_small.FILES)],
)
async def test_vision_engine_without_its_runtime_is_engine_not_installed(
    tmp_path, monkeypatch, engine, files
):
    # Simulate an image built without the vision extra.
    for module in ("torch", "transformers"):
        monkeypatch.setitem(sys.modules, module, None)
    touch_all(tmp_path, files)
    manager = ModelManager(build_registry(tmp_path))
    with pytest.raises(EngineNotInstalledError, match="lacks the engine's runtime"):
        await manager.load(engine)
    assert manager.resident_name is None


def test_weights_guard_allows_ctranslate2_bin_only_when_declared():
    assert_safe_weights_path("align/model.bin", ctranslate2=True)
    for ok in ("a.onnx", "a.data", "a.npz", "a.json"):
        assert_safe_weights_path(ok)
    with pytest.raises(UnsafeWeightsFormatError):
        assert_safe_weights_path("align/model.bin")
    with pytest.raises(UnsafeWeightsFormatError):
        assert_safe_weights_path("conds.pt", ctranslate2=True)


def test_hf_cache_view_has_the_hub_cache_layout(tmp_path):
    source = tmp_path / "models" / "codec.onnx"
    source.parent.mkdir(parents=True)
    source.write_bytes(b"onnx")
    cache = tmp_path / "hub"
    rev = "0" * 40
    snapshot = build_view(cache, "Org/Codec-ONNX", rev, {"codec.onnx": source})
    repo_dir = cache / "models--Org--Codec-ONNX"
    assert (repo_dir / "refs" / "main").read_text() == rev
    assert snapshot == repo_dir / "snapshots" / rev
    assert (snapshot / "codec.onnx").read_bytes() == b"onnx"
    # Rebuilding replaces the links instead of failing.
    build_view(cache, "Org/Codec-ONNX", rev, {"codec.onnx": source})
    assert (snapshot / "codec.onnx").is_symlink()
