"""Minimal WSGI app for import_app tests."""


def app(environ, start_response):
    start_response("200 OK", [])
    return [b"ok"]
