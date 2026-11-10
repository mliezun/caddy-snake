import json

NAME = "beta"


def app(environ, start_response):
    body = json.dumps(
        {
            "app": NAME,
            "script_name": environ["SCRIPT_NAME"],
            "path_info": environ["PATH_INFO"],
        }
    ).encode()
    start_response(
        "200 OK",
        [("Content-Type", "application/json"), ("Content-Length", str(len(body)))],
    )
    return [body]
