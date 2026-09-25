"""Unit tests for ModelManager's single-resident-engine invariant."""

from __future__ import annotations

import pytest

from loomtale_worker.engines.registry import EngineRegistry
from loomtale_worker.model_manager import EngineNotInstalledError, GpuOomError, ModelManager


class FakeEngine:
    def __init__(self, name: str, *, is_installed: bool = True, oom_on_load: bool = False):
        self.name = name
        self.task = "tts"
        self.license = "test"
        self._installed = is_installed
        self._oom_on_load = oom_on_load
        self.load_count = 0
        self.unload_count = 0

    def installed(self) -> bool:
        return self._installed

    async def load(self) -> int:
        self.load_count += 1
        if self._oom_on_load:
            raise RuntimeError("CUDA out of memory. Tried to allocate 2.00 GiB")
        return 1024

    async def unload(self) -> None:
        self.unload_count += 1

    async def run(self, request):
        return request


@pytest.fixture
def registry() -> EngineRegistry:
    return EngineRegistry()


async def test_load_unknown_engine_raises_engine_not_installed(registry):
    manager = ModelManager(registry)
    with pytest.raises(EngineNotInstalledError):
        await manager.load("nonexistent")


async def test_load_uninstalled_engine_raises_engine_not_installed(registry):
    engine = FakeEngine("voice-a", is_installed=False)
    registry.register(engine)
    manager = ModelManager(registry)
    with pytest.raises(EngineNotInstalledError):
        await manager.load("voice-a")


async def test_load_succeeds_and_tracks_resident_name(registry):
    engine = FakeEngine("voice-a")
    registry.register(engine)
    manager = ModelManager(registry)

    held_mb = await manager.load("voice-a")

    assert held_mb == 1024
    assert manager.resident_name == "voice-a"
    assert engine.load_count == 1


async def test_loading_a_different_engine_unloads_the_previous_one(registry):
    a = FakeEngine("voice-a")
    b = FakeEngine("voice-b")
    registry.register(a)
    registry.register(b)
    manager = ModelManager(registry)

    await manager.load("voice-a")
    await manager.load("voice-b")

    assert a.unload_count == 1
    assert manager.resident_name == "voice-b"


async def test_load_translates_cuda_oom_to_gpu_oom_error(registry):
    engine = FakeEngine("voice-a", oom_on_load=True)
    registry.register(engine)
    manager = ModelManager(registry)

    with pytest.raises(GpuOomError):
        await manager.load("voice-a")


async def test_unload_by_name_only_affects_matching_resident(registry):
    engine = FakeEngine("voice-a")
    registry.register(engine)
    manager = ModelManager(registry)
    await manager.load("voice-a")

    await manager.unload("voice-b")  # not resident, no-op
    assert manager.resident_name == "voice-a"

    await manager.unload("voice-a")
    assert manager.resident_name is None
    assert engine.unload_count == 1


async def test_run_auto_loads_when_not_resident(registry):
    engine = FakeEngine("voice-a")
    registry.register(engine)
    manager = ModelManager(registry)

    result = await manager.run("voice-a", "hello")

    assert result == "hello"
    assert engine.load_count == 1
