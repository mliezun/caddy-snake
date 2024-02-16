import uuid

def main(environ, start_response):
    """A simple WSGI application"""
    status = '200 OK'
    response_headers = [('Content-type', 'text/plain')]
    start_response(status, response_headers)
    return [f"Hello World {str(uuid.uuid4())}".encode()]
