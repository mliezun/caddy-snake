import os
import sys
import time
import uuid
from concurrent.futures import ThreadPoolExecutor

import requests
import websocket

BASE_URL = "http://localhost:9080"
WS_URL = "ws://localhost:9080/ws"

# HTTP load (matches other integration apps)
HTTP_POOL_WORKERS = 4

# WebSocket load: many concurrent connections, multiple frames each (text + binary),
# plus one medium-sized binary payload per session for framing pressure.
WS_POOL_WORKERS = 8
WS_SMALL_ROUNDS_PER_CONN = 24
WS_LARGE_BINARY_BYTES = 24 * 1024


def assert_http_hello():
    r = requests.get(f"{BASE_URL}/hello")
    assert r.status_code == 200, r.text
    assert r.content == b"hello esgi"


def assert_http_post_echo():
    payload = b"integration-bytes"
    r = requests.post(f"{BASE_URL}/echo", data=payload)
    assert r.status_code == 200
    assert r.content == payload


def http_roundtrip():
    assert_http_hello()
    assert_http_post_echo()


def test_http_hello():
    assert_http_hello()


def test_http_post_echo():
    assert_http_post_echo()


def test_websocket_echo_text():
    ws = websocket.WebSocket()
    try:
        ws.connect(WS_URL, timeout=10)
        ws.send("ping-esgi")
        reply = ws.recv()
        assert reply == "ECHO:ping-esgi", reply
    finally:
        ws.close()


def test_websocket_echo_binary():
    ws = websocket.WebSocket()
    try:
        ws.connect(WS_URL, timeout=10)
        blob = b"\x00\xff\xfe\x01binary-esgi-\xaa\xbb"
        ws.send(blob, opcode=websocket.ABNF.OPCODE_BINARY)
        reply = ws.recv()
        assert reply == b"ECHO:" + blob, reply
    finally:
        ws.close()


def ws_session(seq: int) -> None:
    """One persistent WebSocket: many small text/binary echoes + one large binary."""
    ws = websocket.WebSocket()
    ws.connect(WS_URL, timeout=120)
    try:
        for i in range(WS_SMALL_ROUNDS_PER_CONN):
            text = f"esgi-{seq}-{i}-{uuid.uuid4().hex}"
            ws.send(text)
            got = ws.recv()
            assert got == f"ECHO:{text}", (got, text)

            blob = text.encode("utf-8") + os.urandom(24)
            ws.send(blob, opcode=websocket.ABNF.OPCODE_BINARY)
            got_b = ws.recv()
            assert got_b == b"ECHO:" + blob, (got_b, blob[:32])

        large = os.urandom(WS_LARGE_BINARY_BYTES)
        ws.send(large, opcode=websocket.ABNF.OPCODE_BINARY)
        got_large = ws.recv()
        assert got_large == b"ECHO:" + large
    finally:
        ws.close()


def make_roundtrips(max_workers: int, count: int):
    start = time.time()
    failed = False

    def item_done(fut):
        exc = fut.exception()
        if exc:
            nonlocal failed
            failed = True
            raise SystemExit(1) from exc

    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        for _ in range(count):
            future = executor.submit(http_roundtrip)
            future.add_done_callback(item_done)

    if failed:
        print("Tests failed")
        exit(1)

    print(f"Completed {count} HTTP roundtrips (hello + POST echo)")
    print(f"Elapsed: {time.time() - start}s")


def make_ws_sessions(max_workers: int, count: int):
    start = time.time()
    failed = False

    def item_done(fut):
        exc = fut.exception()
        if exc:
            nonlocal failed
            failed = True
            raise SystemExit(1) from exc

    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        for seq in range(count):
            future = executor.submit(ws_session, seq)
            future.add_done_callback(item_done)

    if failed:
        print("Tests failed")
        exit(1)

    rounds = WS_SMALL_ROUNDS_PER_CONN
    print(
        f"Completed {count} WebSocket sessions "
        f"({rounds} small text/binary rounds + 1×{WS_LARGE_BINARY_BYTES}b binary each)"
    )
    print(f"Elapsed: {time.time() - start}s")


def main():
    test_websocket_echo_text()
    test_websocket_echo_binary()

    count = int(sys.argv[1]) if len(sys.argv) > 1 else 2_500
    make_roundtrips(max_workers=HTTP_POOL_WORKERS, count=count)
    make_ws_sessions(max_workers=WS_POOL_WORKERS, count=count)

    print("simple_esgi integration tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
