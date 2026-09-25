"""Main entrypoint for the Python worker: a grpc.aio server hosting
worker.proto (control plane), tts/align/train/vision.proto (media RPCs)
and health.proto (liveness), all bearer-token authenticated. No engine is
registered before phases 9a-9c, so every media RPC honestly fails
engine_not_installed until then.
"""

from __future__ import annotations

import argparse
import asyncio
import logging
import os

import grpc

from loomtale.worker.v1 import health_pb2_grpc
from loomtale_worker.auth import BearerTokenInterceptor
from loomtale_worker.engines.registry import EngineRegistry
from loomtale_worker.health_server import WorkerHealthServicer
from loomtale_worker.model_manager import ModelManager
from loomtale_worker.servicers import (
    align_service,
    train_service,
    tts_service,
    vision_service,
    worker_service,
)

logger = logging.getLogger(__name__)

REQUIRED_OFFLINE_ENV = ("HF_HUB_OFFLINE", "TRANSFORMERS_OFFLINE")

# Bounds concurrent in-flight RPCs so a burst of requests cannot exhaust
# this process's threads/memory; generous since only one engine can ever
# be resident (loading serializes on ModelManager's own lock regardless).
MAX_CONCURRENT_RPCS = 32


def assert_offline_env() -> None:
    """Refuses to start unless HF_HUB_OFFLINE/TRANSFORMERS_OFFLINE are
    both set to "1": no engine loader may ever silently reach the
    internet to download weights (the contract's supply-chain
    requirement), so a misconfigured deploy fails loudly at boot instead
    of working "by accident" until the day it doesn't.
    """
    for key in REQUIRED_OFFLINE_ENV:
        if os.environ.get(key) != "1":
            raise RuntimeError(
                f"{key}=1 is required; refusing to start with a possibly-online model loader"
            )


class _AsyncWorkerHealthServicer(health_pb2_grpc.WorkerHealthServicer):
    """Adapts the phase 1 sync WorkerHealthServicer to grpc.aio without
    modifying that existing, already-tested class.
    """

    def __init__(self) -> None:
        self._sync = WorkerHealthServicer()

    async def Check(self, request, context):  # noqa: N802
        return self._sync.Check(request, context)


def build_server(bearer_token: str, manager: ModelManager) -> grpc.aio.Server:
    """Builds and registers every servicer, but does not bind a port:
    callers (serve(), or a test fixture that needs the ephemeral port
    add_insecure_port returns) call that themselves.
    """
    server = grpc.aio.server(
        interceptors=[BearerTokenInterceptor(bearer_token)],
        maximum_concurrent_rpcs=MAX_CONCURRENT_RPCS,
    )
    health_pb2_grpc.add_WorkerHealthServicer_to_server(_AsyncWorkerHealthServicer(), server)
    worker_service.register(server, manager)
    tts_service.register(server, manager)
    align_service.register(server, manager)
    train_service.register(server, manager)
    vision_service.register(server, manager)
    return server


async def serve(bind_addr: str, bearer_token: str) -> None:
    assert_offline_env()
    manager = ModelManager(EngineRegistry())
    server = build_server(bearer_token, manager)
    server.add_insecure_port(bind_addr)
    await server.start()
    logger.info("pyworker listening on %s", bind_addr)
    await server.wait_for_termination()


def main() -> None:
    logging.basicConfig(level=logging.INFO)
    parser = argparse.ArgumentParser(description="Loomtale Python worker gRPC server")
    parser.add_argument("--bind", default="0.0.0.0:9090")
    parser.add_argument(
        "--bearer-token-path", default=os.environ.get("PYWORKER_BEARER_TOKEN_PATH", "")
    )
    args = parser.parse_args()

    if not args.bearer_token_path:
        raise SystemExit("--bearer-token-path (or PYWORKER_BEARER_TOKEN_PATH) is required")
    with open(args.bearer_token_path, encoding="utf-8") as f:
        token = f.read().strip()
    if not token:
        raise SystemExit(f"{args.bearer_token_path} is empty; refusing to start with no token")

    asyncio.run(serve(args.bind, token))


if __name__ == "__main__":
    main()
