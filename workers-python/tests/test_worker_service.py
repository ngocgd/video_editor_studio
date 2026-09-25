"""Unit tests for WorkerServicer against an empty engine registry,
matching the phase 4 contract: ListEngines is empty and LoadModel fails
engine_not_installed until phases 9a-9c register real engines.
"""

from __future__ import annotations

import grpc
import pytest

from loomtale_worker.engines.registry import EngineRegistry
from loomtale_worker.model_manager import ModelManager
from loomtale_worker.servicers.worker_service import WorkerServicer


class AbortSignal(Exception):
    def __init__(self, code, details):
        self.code = code
        self.details = details


class FakeContext:
    async def abort(self, code, details):
        raise AbortSignal(code, details)


async def test_list_engines_is_empty_before_phase_9():
    manager = ModelManager(EngineRegistry())
    servicer = WorkerServicer(manager)

    resp = await servicer.ListEngines(request=None, context=None)

    assert list(resp.engines) == []


async def test_load_model_aborts_engine_not_installed():
    manager = ModelManager(EngineRegistry())
    servicer = WorkerServicer(manager)

    class Request:
        engine = "tts-voice-a"

    with pytest.raises(AbortSignal) as excinfo:
        await servicer.LoadModel(request=Request(), context=FakeContext())

    assert excinfo.value.code == grpc.StatusCode.FAILED_PRECONDITION
    assert "engine_not_installed" in excinfo.value.details


async def test_health_reports_ok():
    manager = ModelManager(EngineRegistry())
    servicer = WorkerServicer(manager)

    resp = await servicer.Health(request=None, context=None)

    assert resp.ok is True


async def test_gpu_status_reports_no_resident_engine_when_none_loaded():
    manager = ModelManager(EngineRegistry())
    servicer = WorkerServicer(manager)

    resp = await servicer.GpuStatus(request=None, context=None)

    assert resp.resident_engine == ""
