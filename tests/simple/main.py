from typing import Callable
import json
import wsgiref.validate

db = {}

def store_item(id: str, content: dict):
    db[id] = content
    return b"Stored"


def get_item(id: str):
    return db.get(id)


def delete_item(id):
    del db[id]
    return b"Deleted"

@wsgiref.validate.validator
def app(environ: dict, start_response: Callable):
    """A simple WSGI application"""
    path: str = environ.get("PATH_INFO", "")
    method: str = environ.get("REQUEST_METHOD", "").lower()
    if path.startswith("/item/"):
        item_id = path[6:]
        body = b""
        status = "200 OK"
        content_type = "text/plain"
        if method == "get":
            body = json.dumps(get_item(item_id)).encode()
            content_type = "application/json"
        elif method == "post":
            request_body = environ["wsgi.input"]
            content = json.loads(request_body.read())
            body = store_item(item_id, content)
        elif method == "delete":
            body = delete_item(item_id)
        else:
            status = "405"
            body = b"Method Not Allowed"
        response_headers = [("Content-Type", content_type)]
        start_response(status, response_headers)
        yield body
    else:
        start_response("404 Not Found", [("Content-type", "text/plain")])
        yield b"Not found"
