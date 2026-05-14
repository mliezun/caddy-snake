import sys

import requests
import websocket

BASE_URL = "http://localhost:9080"
WS_URL = "ws://localhost:9080/ws"


def test_http_hello():
    r = requests.get(f"{BASE_URL}/hello", timeout=10)
    assert r.status_code == 200, r.text
    assert r.content == b"hello esgi"


def test_http_post_echo():
    payload = b"integration-bytes"
    r = requests.post(f"{BASE_URL}/echo", data=payload, timeout=10)
    assert r.status_code == 200
    assert r.content == payload


def test_websocket_echo_text():
    ws = websocket.WebSocket()
    try:
        ws.connect(WS_URL, timeout=10)
        ws.send("ping-esgi")
        reply = ws.recv()
        assert reply == "ECHO:ping-esgi", reply
    finally:
        ws.close()


def main():
    test_http_hello()
    test_http_post_echo()
    test_websocket_echo_text()
    print("simple_esgi integration tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
