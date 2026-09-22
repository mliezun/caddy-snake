"""Minimal WSGI app for exercising the opt-in cluster cache."""

from __future__ import annotations

from http import HTTPStatus
from urllib.parse import parse_qs

from caddysnake import CacheError, cache


def _body(environ) -> bytes:
    try:
        length = int(environ.get("CONTENT_LENGTH") or 0)
    except ValueError:
        length = 0
    return environ["wsgi.input"].read(max(0, min(length, 1 << 20)))


def app(environ, start_response):
    method = (environ.get("REQUEST_METHOD") or "GET").upper()
    path = environ.get("PATH_INFO") or "/"
    query = parse_qs(environ.get("QUERY_STRING") or "")
    key = query.get("key", ["integration-key"])[0].encode()

    def respond(status: int, body: bytes):
        phrase = HTTPStatus(status).phrase
        start_response(f"{status} {phrase}", [("Content-Type", "application/octet-stream")])
        return [body]

    try:
        if path == "/set" and method == "POST":
            ttl = query.get("ttl", [""])[0]
            if ttl:
                cache.set(key, _body(environ), ttl=int(ttl))
            else:
                cache.set(key, _body(environ))
            return respond(200, b"ok")
        if path == "/get" and method == "GET":
            value = cache.get(key)
            return respond(200, b"miss" if value is None else value)
        if path == "/delete" and method == "POST":
            return respond(200, str(cache.delete(key)).encode())
        if path == "/append" and method == "POST":
            cache.append(key, _body(environ))
            return respond(200, b"unexpected success")
    except CacheError as exc:
        return respond(409, str(exc).encode())
    return respond(404, b"not found")
