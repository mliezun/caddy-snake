"""Isolation probe app for integration tests."""

from __future__ import annotations

import json
import os
from pathlib import Path

SECRET_ENV = "CADDYSNAKE_ISOLATION_PROBE_SECRET"
ALLOWED_ENV = "ISOLATION_ALLOWED"


def wsgi_app(environ, start_response):
    path = environ.get("PATH_INFO") or "/"
    qs = environ.get("QUERY_STRING") or ""

    def respond(status: int, body: bytes, ctype: str = "text/plain") -> list[bytes]:
        start_response(f"{status} OK", [("Content-Type", ctype)])
        return [body]

    if path == "/hello":
        return respond(200, b"hello-isolated")

    if path == "/env/probe":
        payload = {
            "probe_secret": os.environ.get(SECRET_ENV, ""),
            "allowed": os.environ.get(ALLOWED_ENV, ""),
        }
        return respond(200, json.dumps(payload).encode(), "application/json")

    if path == "/fs/outside":
        target = qs.split("=", 1)[1] if qs.startswith("path=") else ""
        if not target:
            return respond(400, b"missing path query")
        try:
            data = Path(target).read_text(encoding="utf-8")
        except OSError as exc:
            return respond(403, str(exc).encode())
        return respond(200, data.encode())

    return respond(404, b"not found")
