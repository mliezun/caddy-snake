"""HTTP integration tests for caddysnake.cache (via Caddy workers)."""

from __future__ import annotations

import threading
import time

import requests

BASE_URL = "http://localhost:9080"


def test_wsgi_cross_worker_blocking_pop():
    """Two WSGI workers share one cache: blocking pop on one worker unblocks after append on another."""
    r = requests.post(f"{BASE_URL}/share/reset", timeout=10)
    assert r.status_code == 200, r.text

    out: list[bytes] = []

    def popper():
        resp = requests.get(f"{BASE_URL}/share/pop", timeout=130)
        out.append(resp.content)
        assert resp.status_code == 200, resp.text

    th = threading.Thread(target=popper)
    th.start()
    time.sleep(0.4)
    r2 = requests.post(f"{BASE_URL}/share/append", data=b"cross-worker", timeout=10)
    assert r2.status_code == 200, r2.text
    th.join(timeout=135)
    assert not th.is_alive()
    assert out == [b"cross-worker"]


def test_asgi_aget_and_list():
    requests.post(f"{BASE_URL}/asgi/set?k=ak2", data=b"v1", timeout=10).raise_for_status()
    g = requests.get(f"{BASE_URL}/asgi/get?k=ak2", timeout=10)
    assert g.status_code == 200
    assert g.content == b"v1"

    requests.post(f"{BASE_URL}/asgi/append?k=al", data=b"a", timeout=10).raise_for_status()
    requests.post(f"{BASE_URL}/asgi/append?k=al", data=b"b", timeout=10).raise_for_status()
    g2 = requests.get(f"{BASE_URL}/asgi/get?k=al", timeout=10)
    assert g2.status_code == 200
    assert g2.json() == ["a", "b"]

    pop = requests.get(f"{BASE_URL}/asgi/pop?k=al&t=0.05", timeout=10)
    assert pop.status_code == 200
    assert pop.content == b"a"

    pop2 = requests.get(f"{BASE_URL}/asgi/pop?k=al&t=0.05", timeout=10)
    assert pop2.status_code == 200
    assert pop2.content == b"b"

    empty = requests.get(f"{BASE_URL}/asgi/get?k=al", timeout=10)
    assert empty.json() == []


def test_esgi_cache():
    r = requests.post(f"{BASE_URL}/esgi/set?k=ek", data=b"esgi-bytes", timeout=10)
    assert r.status_code == 200, r.text
    g = requests.get(f"{BASE_URL}/esgi/get?k=ek", timeout=10)
    assert g.status_code == 200
    assert g.content == b"esgi-bytes"

    requests.post(f"{BASE_URL}/esgi/append?k=eq", data=b"p1", timeout=10).raise_for_status()
    requests.post(f"{BASE_URL}/esgi/append?k=eq", data=b"p2", timeout=10).raise_for_status()
    lst = requests.get(f"{BASE_URL}/esgi/get?k=eq", timeout=10)
    assert lst.status_code == 200
    assert lst.json() == ["p1", "p2"]


if __name__ == "__main__":
    test_wsgi_cross_worker_blocking_pop()
    test_asgi_aget_and_list()
    test_esgi_cache()
    print("simple_cache integration tests passed")
