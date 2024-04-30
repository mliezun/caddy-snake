import uuid


def main(environ, start_response):
    """A simple WSGI application"""
    status = "200 OK"
    response_headers = [
        ("Content-type", "text/plain"),
        ("X-Custom-Header", "Custom-Value"),
    ]
    start_response(status, response_headers)
    yield f"Simple app: {str(uuid.uuid4())}".encode()
