"""Minimal ASGI app for import_app tests."""


async def app(scope, receive, send):
    await send(
        {
            "type": "http.response.start",
            "status": 200,
            "headers": [(b"Content-Type", b"text/plain")],
        }
    )
    await send({"type": "http.response.body", "body": b"ok"})
