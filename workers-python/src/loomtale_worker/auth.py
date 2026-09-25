"""Bearer-token authentication for every RPC: the worker is reachable
only from the Go worker process's network segment, but the shared secret
is still checked per the contract ("A shared bearer token travels in
metadata").
"""

from __future__ import annotations

import hmac

import grpc


class BearerTokenInterceptor(grpc.aio.ServerInterceptor):
    """Rejects any call whose "authorization" metadata does not exactly
    match "Bearer {token}", using a constant-time comparison. Wraps the
    real handler rather than replacing it outright, so unary and
    streaming RPCs (Synthesize/Align/Train all stream responses) are
    both handled correctly.
    """

    def __init__(self, token: str) -> None:
        self._token = token

    async def intercept_service(self, continuation, handler_call_details):
        handler = await continuation(handler_call_details)
        if handler is None:
            return None

        metadata = dict(handler_call_details.invocation_metadata or [])
        auth = metadata.get("authorization", "")
        expected = f"Bearer {self._token}"
        if hmac.compare_digest(auth, expected):
            return handler

        async def deny_unary(_request, context: grpc.aio.ServicerContext):
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "invalid or missing bearer token")

        async def deny_stream(_request, context: grpc.aio.ServicerContext):
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "invalid or missing bearer token")
            return
            yield  # pragma: no cover - unreachable, makes this an async generator

        if handler.response_streaming:
            return grpc.unary_stream_rpc_method_handler(
                deny_stream,
                request_deserializer=handler.request_deserializer,
                response_serializer=handler.response_serializer,
            )
        return grpc.unary_unary_rpc_method_handler(
            deny_unary,
            request_deserializer=handler.request_deserializer,
            response_serializer=handler.response_serializer,
        )
