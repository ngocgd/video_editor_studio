"""Engine registry: maps an engine name to its Engine instance. Empty
until phases 9a-9c register real engines; every lookup against an empty
(or simply unknown-name) registry is the intended, honest
engine_not_installed path documented in the phase 4 contract.
"""

from __future__ import annotations

from loomtale_worker.engines.base import Engine


class EngineRegistry:
    """A process-wide, in-memory map of engine name -> Engine. No engine
    is registered by this phase; ModelManager and the RPC servicers only
    ever see EngineNotInstalledError for a name absent here.
    """

    def __init__(self) -> None:
        self._engines: dict[str, Engine] = {}

    def register(self, engine: Engine) -> None:
        self._engines[engine.name] = engine

    def get(self, name: str) -> Engine | None:
        return self._engines.get(name)

    def list(self) -> list[Engine]:
        return list(self._engines.values())
