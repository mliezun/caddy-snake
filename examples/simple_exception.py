import sys


def main(environ, start_response):
    """A simple WSGI application that passes exc_info to start_response"""
    status = "200 OK"
    response_headers = [("Content-type", "text/plain")]
    try:
        start_response(status, response_headers + 1)
    except TypeError as e:
        start_response(status, response_headers, sys.exc_info())
    return [b"Simple Exception"]
