"""WSGI app that echoes the scheme and forwarded headers it receives."""

import json


def app(environ, start_response):
    body = json.dumps(
        {
            "scheme": environ.get("wsgi.url_scheme"),
            "x-forwarded-proto": environ.get("HTTP_X_FORWARDED_PROTO"),
            "x-forwarded-host": environ.get("HTTP_X_FORWARDED_HOST"),
            "x-forwarded-for": environ.get("HTTP_X_FORWARDED_FOR"),
        }
    ).encode()
    start_response(
        "200 OK",
        [("Content-Type", "application/json"), ("Content-Length", str(len(body)))],
    )
    return [body]
