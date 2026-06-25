import os

ALLOWED_ENV_VARS = frozenset(
    {
        "APP_TEST_FROM_FILE",
        "APP_TEST_INLINE",
        "APP_TEST_OVERRIDE",
    }
)


async def app(scope, receive, send):
    if scope["type"] != "http":
        return

    path = scope["path"]
    if path == "/hello":
        await send(
            {
                "type": "http.response.start",
                "status": 200,
                "headers": [(b"content-type", b"text/plain")],
            }
        )
        await send({"type": "http.response.body", "body": b"Hello from app1"})
        return

    if path.startswith("/env/"):
        name = path[5:]
        if name not in ALLOWED_ENV_VARS:
            await send(
                {
                    "type": "http.response.start",
                    "status": 404,
                    "headers": [(b"content-type", b"text/plain")],
                }
            )
            await send({"type": "http.response.body", "body": b"not found"})
            return
        value = os.environ.get(name)
        if value is None:
            await send(
                {
                    "type": "http.response.start",
                    "status": 404,
                    "headers": [(b"content-type", b"text/plain")],
                }
            )
            await send({"type": "http.response.body", "body": b"unset"})
            return
        body = value.encode()
        await send(
            {
                "type": "http.response.start",
                "status": 200,
                "headers": [(b"content-type", b"text/plain")],
            }
        )
        await send({"type": "http.response.body", "body": body})
        return

    await send(
        {
            "type": "http.response.start",
            "status": 404,
            "headers": [(b"content-type", b"text/plain")],
        }
    )
    await send({"type": "http.response.body", "body": b"Not Found"})
