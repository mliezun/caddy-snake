"""Minimal WSGI/ASGI/ESGI app with optional import-time startup delay."""

from __future__ import annotations

import os
import time

_delay = float(os.environ.get("START_DELAY", "0") or "0")
if _delay > 0:
    time.sleep(_delay)


def wsgi_app(environ, start_response):
    start_response("200 OK", [("Content-Type", "text/plain; charset=utf-8")])
    return [b"ok-wsgi"]


async def asgi_app(scope, receive, send):
    if scope["type"] != "http":
        return
    await send(
        {
            "type": "http.response.start",
            "status": 200,
            "headers": [(b"content-type", b"text/plain; charset=utf-8")],
        }
    )
    await send({"type": "http.response.body", "body": b"ok-asgi"})


def application(scope, protocol):
    """ESGI application entrypoint."""
    if scope.get("proto") != "http":
        protocol.response_bytes(500, [("Content-Type", "text/plain")], b"bad scope")
        return
    protocol.response_bytes(
        200,
        [("Content-Type", "text/plain; charset=utf-8")],
        b"ok-esgi",
    )
