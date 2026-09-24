"""gRPC health server backing the WorkerHealth service from health.proto."""

from __future__ import annotations

import argparse
import logging
from concurrent import futures

import grpc

from loomtale.worker.v1 import health_pb2, health_pb2_grpc

logger = logging.getLogger(__name__)


class WorkerHealthServicer(health_pb2_grpc.WorkerHealthServicer):
    """Answers Check() with SERVING; later phases wire real dependency checks."""

    def Check(
        self, request: health_pb2.HealthCheckRequest, context: grpc.ServicerContext
    ) -> health_pb2.HealthCheckResponse:
        del request, context
        return health_pb2.HealthCheckResponse(
            status=health_pb2.HealthCheckResponse.Status.STATUS_SERVING
        )


def build_server(bind_addr: str, max_workers: int = 4) -> grpc.Server:
    """Construct (but do not start) a gRPC server bound to bind_addr."""
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=max_workers))
    health_pb2_grpc.add_WorkerHealthServicer_to_server(WorkerHealthServicer(), server)
    server.add_insecure_port(bind_addr)
    return server


def main() -> None:
    logging.basicConfig(level=logging.INFO)
    parser = argparse.ArgumentParser(description="Loomtale worker health gRPC server")
    parser.add_argument("--bind", default="0.0.0.0:50051")
    args = parser.parse_args()

    server = build_server(args.bind)
    server.start()
    logger.info("worker health server listening on %s", args.bind)
    server.wait_for_termination()


if __name__ == "__main__":
    main()
