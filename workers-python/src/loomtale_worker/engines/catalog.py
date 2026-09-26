"""The engines this worker hosts. Each one describes itself and reports
installed only when its pinned files are on the models volume; nothing
heavy is imported until an engine loads, so the base image (without the
tts-en, tts-vi, align, vision and train extras) still lists every engine
and answers an honest engine_not_installed for the ones it cannot run.
"""

from __future__ import annotations

from pathlib import Path

from loomtale_worker.engines.chatterbox import ChatterboxEngine
from loomtale_worker.engines.depth_small import DepthSmallEngine
from loomtale_worker.engines.dinov2_score import Dinov2ScoreEngine
from loomtale_worker.engines.registry import EngineRegistry
from loomtale_worker.engines.vieneu import VieneuEngine
from loomtale_worker.engines.whisper_align import WhisperAlignEngine


def build_registry(models_root: Path) -> EngineRegistry:
    registry = EngineRegistry()
    for engine in (
        ChatterboxEngine(models_root),
        VieneuEngine(models_root),
        WhisperAlignEngine(models_root),
        Dinov2ScoreEngine(models_root),
        DepthSmallEngine(models_root),
    ):
        registry.register(engine)
    return registry
