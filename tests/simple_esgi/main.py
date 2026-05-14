"""Minimal ESGI app (spec 0.1-draft) for integration tests."""


def application(scope, protocol):
    if scope["proto"] == "http":
        if scope["path"] == "/hello" and scope["method"] == "GET":
            protocol.response_bytes(
                200,
                [("Content-Type", "text/plain; charset=utf-8")],
                b"hello esgi",
            )
            return
        if scope["path"] == "/echo" and scope["method"] == "POST":
            body = protocol.read_body()
            protocol.response_bytes(
                200,
                [("Content-Type", "application/octet-stream")],
                body,
            )
            return
        protocol.response_bytes(404, [("Content-Type", "text/plain")], b"not found")
        return

    if scope["proto"] == "ws":
        if scope["path"] != "/ws":
            protocol.close(code=1008, reason="invalid path")
            return
        ws = protocol.accept()
        while True:
            msg = ws.receive()
            if msg.kind == 0:
                break
            if msg.kind == 2:
                ws.send_str(f"ECHO:{msg.data}")
            elif msg.kind == 1:
                ws.send_bytes(b"ECHO:" + msg.data)
        protocol.close()
        return

    protocol.response_bytes(500, [("Content-Type", "text/plain")], b"bad scope")
