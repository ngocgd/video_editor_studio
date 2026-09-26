"""End-to-end engine checks on the host GPU with the real weights:
`uv run --extra tts-en --extra tts-vi --extra align pytest -m gpu` (the
vision and train extras for the score, depth and training checks) with
MODELS_DIR pointing at the models volume. Skipped wherever the weights
or a runtime are missing, so the default test run stays offline."""

from __future__ import annotations

import importlib.util

import numpy as np
import pytest

from loomtale_worker.engines.aitoolkit_train import ai_toolkit_dir
from loomtale_worker.engines.audio import to_wav_bytes
from loomtale_worker.engines.catalog import build_registry
from loomtale_worker.engines.jobs import AlignJob, DepthJob, ScoreJob, SynthesisJob, TrainJob
from loomtale_worker.engines.local_files import models_dir
from loomtale_worker.model_manager import ModelManager

pytestmark = pytest.mark.gpu


def manager_for(engine: str, module: str) -> ModelManager:
    registry = build_registry(models_dir())
    if not registry.get(engine).installed():
        pytest.skip(f"{engine} weights are not under {models_dir()}")
    if importlib.util.find_spec(module) is None:
        pytest.skip(f"{module} is not installed (engine extra missing)")
    return ModelManager(registry)


async def test_vieneu_speaks_vietnamese_with_a_builtin_voice():
    manager = manager_for("vieneu-v3-turbo", "vieneu")
    out = await manager.run(
        "vieneu-v3-turbo",
        SynthesisJob(text="Xin chào, đây là một câu thử.", language="vi", voice="Mai Anh"),
    )
    assert out.sample_rate == 48_000
    assert 0.5 < out.duration_s < 10
    await manager.unload()


async def test_chatterbox_clones_a_reference_and_align_times_it(tmp_path):
    vi = manager_for("vieneu-v3-turbo", "vieneu")
    ref = await vi.run(
        "vieneu-v3-turbo",
        SynthesisJob(
            text="Xin chào các bạn, hôm nay trời rất đẹp.", language="vi", voice="Mai Anh"
        ),
    )
    await vi.unload()
    reference = tmp_path / "ref.wav"
    reference.write_bytes(to_wav_bytes(ref.samples, ref.sample_rate))

    en = manager_for("chatterbox", "chatterbox")
    text = "The old master raised his sword. The mountain answered with thunder."
    speech = await en.run(
        "chatterbox", SynthesisJob(text=text, language="en", reference_wav=reference)
    )
    await en.unload()
    assert np.max(np.abs(speech.samples)) > 0.01
    narration = tmp_path / "narration.wav"
    narration.write_bytes(to_wav_bytes(speech.samples, speech.sample_rate))

    align = manager_for("whisper-align", "faster_whisper")
    cues = await align.run(
        "whisper-align", AlignJob(audio_path=narration, text=text, language="en")
    )
    await align.unload()
    assert cues.granularity == "word"
    assert len(cues.segments) == 2
    assert cues.segments[-1].end <= speech.duration_s + 0.5


def write_pictures(root, count: int) -> list:
    """Small distinct RGB pictures (PIL comes with the vision and train
    extras)."""
    from PIL import Image

    root.mkdir(parents=True, exist_ok=True)
    paths = []
    rng = np.random.default_rng(7)
    for i in range(count):
        pixels = rng.integers(0, 255, size=(512, 512, 3), dtype=np.uint8)
        path = root / f"ref-{i}.png"
        Image.fromarray(pixels).save(path)
        paths.append(path)
    return paths


async def test_dinov2_scores_an_image_against_itself_highest(tmp_path):
    manager = manager_for("dinov2-base", "transformers")
    image, other = write_pictures(tmp_path, 2)
    same = await manager.run("dinov2-base", ScoreJob(image_path=image, reference_paths=[image]))
    mixed = await manager.run(
        "dinov2-base", ScoreJob(image_path=image, reference_paths=[image, other])
    )
    assert same.score > 0.99
    assert mixed.score < same.score


async def test_depth_small_returns_a_png_the_size_of_the_input(tmp_path):
    manager = manager_for("depth-anything-v2-small", "transformers")
    (image,) = write_pictures(tmp_path, 1)
    out = await manager.run("depth-anything-v2-small", DepthJob(image_path=image))
    assert (out.width, out.height) == (512, 512)
    assert out.png.startswith(b"\x89PNG")


async def test_trainer_smoke_run_writes_lora_weights(tmp_path):
    manager = manager_for("z-image-turbo-trainer", "diffusers")
    if not (ai_toolkit_dir() / "run.py").is_file():
        pytest.skip(f"no ai-toolkit checkout at {ai_toolkit_dir()}")
    write_pictures(tmp_path / "dataset", 4)
    steps: list[int] = []
    job = TrainJob(
        dataset_dir=tmp_path / "dataset",
        work_dir=tmp_path,
        base_model="z-image-turbo",
        params={"steps": "10", "rank": "4"},
        step=lambda s, _t: steps.append(s),
    )
    out = await manager.run("z-image-turbo-trainer", job)
    assert out.weights.stat().st_size > 0
    assert steps and steps[-1] == 10
