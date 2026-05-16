"""Exercise caddysnake.cache across WSGI (2 workers), ASGI, and ESGI routes."""

from __future__ import annotations

import json
from urllib.parse import parse_qs

from caddysnake import cache


def _wsgi_read_body(environ, max_len: int = 1 << 20) -> bytes:
    try:
        n = int(environ.get("CONTENT_LENGTH") or 0)
    except ValueError:
        n = 0
    if n <= 0:
        return b""
    if n > max_len:
        n = max_len
    return environ["wsgi.input"].read(n)


def wsgi_app(environ, start_response):
    path = environ.get("PATH_INFO") or "/"
    method = (environ.get("REQUEST_METHOD") or "GET").upper()

    def respond(status: int, body: bytes, ctype: str = "text/plain") -> list[bytes]:
        reason = {200: "OK", 404: "Not Found", 500: "Internal Server Error"}.get(
            status, "OK"
        )
        start_response(f"{status} {reason}", [("Content-Type", ctype)])
        return [body]

    if path == "/share/reset" and method == "POST":
        cache.delete("q")
        cache.append("q", b"_")
        cache.pop("q")
        return respond(200, b"ok")

    if path == "/share/pop" and method == "GET":
        v = cache.pop("q", timeout=120.0)
        if v is None:
            return respond(200, b"nil")
        return respond(200, v, "application/octet-stream")

    if path == "/share/append" and method == "POST":
        cache.append("q", _wsgi_read_body(environ))
        return respond(200, b"ok")

    return respond(404, b"not found")


async def asgi_app(scope, receive, send):
    if scope["type"] != "http":
        await send({"type": "http.response.start", "status": 500, "headers": []})
        await send({"type": "http.response.body", "body": b""})
        return

    path = scope["path"]
    method = scope["method"].upper()

    async def read_body() -> bytes:
        b = b""
        more = True
        while more:
            msg = await receive()
            if msg["type"] == "http.request":
                b += msg.get("body", b"")
                more = msg.get("more_body", False)
        return b

    async def send_resp(status: int, body: bytes, ctype: bytes = b"text/plain"):
        await send(
            {
                "type": "http.response.start",
                "status": status,
                "headers": [(b"content-type", ctype)],
            }
        )
        await send({"type": "http.response.body", "body": body})

    qs = parse_qs(scope.get("query_string", b"").decode())

    if path == "/asgi/set" and method == "POST":
        body = await read_body()
        key = (qs.get("k", ["ak"])[0]).encode()
        ttl_raw = qs.get("ttl", [""])[0]
        if ttl_raw == "":
            cache.set(key, body)
        else:
            cache.set(key, body, ttl=int(ttl_raw))
        await send_resp(200, b"ok")
        return

    if path == "/asgi/get" and method == "GET":
        key = (qs.get("k", ["ak"])[0]).encode()
        v = await cache.aget(key)
        if v is None:
            await send_resp(200, b"miss")
        elif isinstance(v, list):
            await send_resp(
                200,
                json.dumps([x.decode("latin1") for x in v]).encode(),
                b"application/json",
            )
        else:
            await send_resp(200, v)
        return

    if path == "/asgi/append" and method == "POST":
        key = (qs.get("k", ["ak"])[0]).encode()
        body = await read_body()
        await cache.aappend(key, body)
        await send_resp(200, b"ok")
        return

    if path == "/asgi/pop" and method == "GET":
        key = (qs.get("k", ["q"])[0]).encode()
        tout = qs.get("t", [""])[0]
        timeout = float(tout) if tout != "" else None
        v = await cache.apop(key, timeout=timeout)
        if v is None:
            await send_resp(200, b"nil")
        else:
            await send_resp(200, v)
        return

    await send_resp(404, b"not found")


def application(scope, protocol):
    if scope["proto"] != "http":
        protocol.response_bytes(500, [("Content-Type", "text/plain")], b"bad proto")
        return
    path = scope["path"]
    method = scope["method"].upper()
    raw_qs = scope.get("query_string", "") or ""
    if isinstance(raw_qs, bytes):
        raw_qs = raw_qs.decode()
    qs = parse_qs(raw_qs)

    def resp(status: int, body: bytes, ctype: str = "text/plain"):
        protocol.response_bytes(status, [("Content-Type", ctype)], body)

    if path == "/esgi/set" and method == "POST":
        key = (qs.get("k", ["ek"])[0]).encode()
        cache.set(key, protocol.read_body())
        resp(200, b"ok")
        return
    if path == "/esgi/get" and method == "GET":
        key = (qs.get("k", ["ek"])[0]).encode()
        v = cache.get(key)
        if v is None:
            resp(200, b"miss")
        elif isinstance(v, list):
            resp(
                200,
                json.dumps([e.decode("latin1") for e in v]).encode(),
                "application/json",
            )
        else:
            resp(200, v, "application/octet-stream")
        return
    if path == "/esgi/append" and method == "POST":
        key = (qs.get("k", ["eq"])[0]).encode()
        cache.append(key, protocol.read_body())
        resp(200, b"ok")
        return
    resp(404, b"not found")
