"""Unit test for the worker health gRPC servicer (no network)."""

from loomtale.worker.v1 import health_pb2
from loomtale_worker.health_server import WorkerHealthServicer


def test_check_reports_serving():
    servicer = WorkerHealthServicer()
    response = servicer.Check(health_pb2.HealthCheckRequest(), context=None)
    assert response.status == health_pb2.HealthCheckResponse.Status.STATUS_SERVING
