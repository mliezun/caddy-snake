"""Minimal ESGI app for benchmarks (JSON /hello, parity with Flask/FastAPI examples)."""

from __future__ import annotations

import json


def application(scope, protocol):
    if scope["proto"] != "http":
        protocol.response_bytes(
            500,
            [("Content-Type", "text/plain; charset=utf-8")],
            b"unsupported proto",
        )
        return

    method = scope.get("method", "GET").upper()
    path = scope.get("path") or "/"
    if method == "GET" and path == "/hello":
        body = json.dumps({"message": "Hello, World!"}).encode("utf-8")
        protocol.response_bytes(
            200,
            [("Content-Type", "application/json")],
            body,
        )
        return

    protocol.response_bytes(
        404,
        [("Content-Type", "application/json")],
        b'{"detail":"Not Found"}',
    )
