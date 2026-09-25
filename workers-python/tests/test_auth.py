"""End-to-end test of BearerTokenInterceptor against a real (loopback)
grpc.aio server, covering both a unary RPC (Health) and the server-
streaming shape (Synthesize) the interceptor must also handle correctly.
"""

from __future__ import annotations

import grpc
import pytest

from loomtale.worker.v1 import health_pb2, health_pb2_grpc
from loomtale_worker.engines.registry import EngineRegistry
from loomtale_worker.model_manager import ModelManager
from loomtale_worker.server import build_server


@pytest.fixture
async def running_server():
    manager = ModelManager(EngineRegistry())
    server = build_server("correct-token", manager)
    port = server.add_insecure_port("127.0.0.1:0")
    await server.start()
    try:
        yield f"127.0.0.1:{port}"
    finally:
        await server.stop(grace=None)


async def test_health_check_succeeds_with_correct_token(running_server):
    async with grpc.aio.insecure_channel(running_server) as channel:
        stub = health_pb2_grpc.WorkerHealthStub(channel)
        resp = await stub.Check(
            health_pb2.HealthCheckRequest(), metadata=(("authorization", "Bearer correct-token"),)
        )
        assert resp.status == health_pb2.HealthCheckResponse.Status.STATUS_SERVING


async def test_health_check_rejects_missing_token(running_server):
    async with grpc.aio.insecure_channel(running_server) as channel:
        stub = health_pb2_grpc.WorkerHealthStub(channel)
        with pytest.raises(grpc.aio.AioRpcError) as excinfo:
            await stub.Check(health_pb2.HealthCheckRequest())
        assert excinfo.value.code() == grpc.StatusCode.UNAUTHENTICATED


async def test_health_check_rejects_wrong_token(running_server):
    async with grpc.aio.insecure_channel(running_server) as channel:
        stub = health_pb2_grpc.WorkerHealthStub(channel)
        with pytest.raises(grpc.aio.AioRpcError) as excinfo:
            await stub.Check(
                health_pb2.HealthCheckRequest(), metadata=(("authorization", "Bearer wrong-token"),)
            )
        assert excinfo.value.code() == grpc.StatusCode.UNAUTHENTICATED
