import json


async def app(scope, receive, send):
    if scope["type"] != "http":
        return
    body = json.dumps(
        {"app": "gamma", "root_path": scope["root_path"], "path": scope["path"]}
    ).encode()
    await send(
        {
            "type": "http.response.start",
            "status": 200,
            "headers": [
                (b"content-type", b"application/json"),
                (b"content-length", str(len(body)).encode()),
            ],
        }
    )
    await send({"type": "http.response.body", "body": body})
