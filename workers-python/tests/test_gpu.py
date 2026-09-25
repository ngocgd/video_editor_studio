"""Unit test for the pynvml-based GPU probe: it must never raise, even
when no NVIDIA driver/GPU is present (the toolbox test container has
none), matching the "no_gpu handled" contract requirement.
"""

from __future__ import annotations

from loomtale_worker.gpu import GpuSnapshot, probe


def test_probe_never_raises_and_returns_a_snapshot():
    snap = probe()
    assert isinstance(snap, GpuSnapshot)
    if not snap.present:
        assert snap.total_mb == 0
        assert snap.free_mb == 0
