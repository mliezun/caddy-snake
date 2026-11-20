"""ASGI app that echoes the scheme and forwarded headers it receives."""

import json


async def app(scope, receive, send):
    if scope["type"] != "http":
        return
    headers = {k.decode("latin-1"): v.decode("latin-1") for k, v in scope["headers"]}
    body = json.dumps(
        {
            "scheme": scope["scheme"],
            "x-forwarded-proto": headers.get("x-forwarded-proto"),
            "x-forwarded-host": headers.get("x-forwarded-host"),
            "x-forwarded-for": headers.get("x-forwarded-for"),
        }
    ).encode()
    await send(
        {
            "type": "http.response.start",
            "status": 200,
            "headers": [(b"content-type", b"application/json")],
        }
    )
    await send({"type": "http.response.body", "body": body})
