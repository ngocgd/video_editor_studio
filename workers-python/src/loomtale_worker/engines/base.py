"""Engine protocol every real backend (phases 9a-9c) implements. Empty
until then: the registry (registry.py) starts with no engines, so every
call routes to the honest engine_not_installed failure path.
"""

from __future__ import annotations

from typing import Any, Protocol


class Engine(Protocol):
    """A single loadable inference engine (TTS voice, align model, LoRA
    trainer, vision scorer, ...). Instances are constructed once at
    process startup (see registry.py) and describe themselves without
    loading any weights until load() is called.
    """

    name: str
    task: str  # "tts" | "align" | "train" | "vision"
    license: str

    def installed(self) -> bool:
        """Whether this engine's weights exist on disk (HF_HUB_OFFLINE=1
        means "on disk already", never "downloadable now")."""
        ...

    async def load(self) -> int:
        """Loads weights into VRAM/RAM; returns the held VRAM in MB."""
        ...

    async def unload(self) -> None:
        """Releases whatever this engine currently holds resident."""
        ...

    async def run(self, request: Any) -> Any:  # noqa: ANN401 - per-task request/response shape
        """Executes one request against the loaded model."""
        ...
