import json

db = {}


def store_item(id: str, content: dict):
    db[id] = content
    return b"Stored"


def get_item(id: str):
    return db.get(id)


def delete_item(id):
    del db[id]
    return b"Deleted"


async def app(scope, receive, send):
    path: str = scope["path"]
    method: str = scope["method"].lower()
    if path.startswith("/item/"):
        item_id = path[6:]
        body = b""
        status = 200
        content_type = b"text/plain"
        if method == "get":
            body = json.dumps(get_item(item_id)).encode()
            content_type = b"application/json"
        elif method == "post":
            request_body = await receive()
            content = request_body["body"]
            while request_body["more_body"]:
                request_body = await receive()
                content += request_body["body"]
            body = store_item(item_id, json.loads(content))
        elif method == "delete":
            body = delete_item(item_id)
        else:
            status = 405
            body = b"Method Not Allowed"
        await send(
            {
                "type": "http.response.start",
                "status": status,
                "headers": [(b"Content-Type", content_type)],
            }
        )
        await send({"type": "http.response.body", "body": body})
    else:
        await send(
            {
                "type": "http.response.start",
                "status": 404,
                "headers": [(b"Content-Type", b"text/plain")],
            }
        )
        await send({"type": "http.response.body", "body": b"Not Found"})
