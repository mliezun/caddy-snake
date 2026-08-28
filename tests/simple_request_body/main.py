"""ASGI app that streams the request body and returns the byte count."""


async def app(scope, receive, send):
    if scope["type"] != "http":
        return
    n = 0
    more = True
    while more:
        event = await receive()
        if event["type"] != "http.request":
            break
        n += len(event.get("body") or b"")
        more = bool(event.get("more_body", False))
    body = str(n).encode("ascii")
    await send(
        {
            "type": "http.response.start",
            "status": 200,
            "headers": [(b"content-type", b"text/plain")],
        }
    )
    await send({"type": "http.response.body", "body": body})
