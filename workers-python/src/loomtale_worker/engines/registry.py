"""Engine registry: maps an engine name to its Engine instance. A lookup
of an unknown name is the intended, honest engine_not_installed path.
"""

from __future__ import annotations

from loomtale_worker.engines.base import Engine


class EngineRegistry:
    """A process-wide, in-memory map of engine name -> Engine, filled
    once at startup by catalog.build_registry.
    """

    def __init__(self) -> None:
        self._engines: dict[str, Engine] = {}

    def register(self, engine: Engine) -> None:
        self._engines[engine.name] = engine

    def get(self, name: str) -> Engine | None:
        return self._engines.get(name)

    def list(self) -> list[Engine]:
        return list(self._engines.values())
