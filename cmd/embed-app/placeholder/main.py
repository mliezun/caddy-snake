# Placeholder app — used when building embed-app without a user-provided app.zip.
# Replace with your own app via: ./build.sh /path/to/your-app.zip 3.13


def app(environ, start_response):  # noqa: D401
    """Minimal WSGI app (stdlib only, no deps)."""
    start_response("200 OK", [("Content-Type", "text/plain")])
    return [
        b"Replace this placeholder with your app. Run: ./build.sh /path/to/app.zip 3.13"
    ]
