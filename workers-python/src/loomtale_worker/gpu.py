"""pynvml-based VRAM probe, used to answer GpuStatus and to let engines
check headroom before loading. A missing/broken NVML (no GPU present, or
running outside a GPU-passthrough container) is treated as "no GPU", not
an error: every caller here degrades to reporting gpu_present=False
rather than crashing the process.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class GpuSnapshot:
    """Aggregate (not per-process) VRAM, matching the Go residency
    manager's own nvidia-smi probe: both intentionally avoid
    per-process attribution, which is unreliable under WDDM.
    """

    present: bool
    total_mb: int = 0
    free_mb: int = 0


def probe() -> GpuSnapshot:
    """Reads total/free VRAM for the first GPU via pynvml. Any failure
    (pynvml not installed, driver not initialized, no device) is caught
    and reported as GpuSnapshot(present=False), per the "no_gpu handled"
    contract requirement.
    """
    try:
        import pynvml
    except ImportError:
        logger.info("pynvml not installed; reporting no_gpu")
        return GpuSnapshot(present=False)

    try:
        pynvml.nvmlInit()
        try:
            handle = pynvml.nvmlDeviceGetHandleByIndex(0)
            info = pynvml.nvmlDeviceGetMemoryInfo(handle)
            return GpuSnapshot(
                present=True,
                total_mb=info.total // (1024 * 1024),
                free_mb=info.free // (1024 * 1024),
            )
        finally:
            pynvml.nvmlShutdown()
    except Exception:  # noqa: BLE001 - any NVML failure means "no GPU here"
        logger.info("nvidia driver/NVML unavailable; reporting no_gpu", exc_info=True)
        return GpuSnapshot(present=False)
