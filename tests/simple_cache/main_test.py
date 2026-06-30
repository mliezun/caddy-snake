"""HTTP integration tests for caddysnake.cache (via Caddy workers)."""

from __future__ import annotations

import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

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


def test_cross_worker_set_add_members():
    requests.post(f"{BASE_URL}/share/sadd?k=itgrp", data=b"w0", timeout=10).raise_for_status()
    requests.post(f"{BASE_URL}/share/sadd?k=itgrp", data=b"w1", timeout=10).raise_for_status()
    got = requests.get(f"{BASE_URL}/share/smembers?k=itgrp", timeout=10)
    assert got.status_code == 200
    assert set(got.json()) == {"w0", "w1"}


def test_cross_worker_set_remove():
    requests.post(f"{BASE_URL}/share/sadd?k=rmgrp", data=b"a", timeout=10).raise_for_status()
    requests.post(f"{BASE_URL}/share/sadd?k=rmgrp", data=b"b", timeout=10).raise_for_status()
    requests.post(f"{BASE_URL}/share/srem?k=rmgrp", data=b"a", timeout=10).raise_for_status()
    got = requests.get(f"{BASE_URL}/share/smembers?k=rmgrp", timeout=10)
    assert got.json() == ["b"]


def test_cross_worker_setnx_lock_single_winner():
    key = f"lock-{time.time_ns()}"

    def try_lock():
        return requests.post(f"{BASE_URL}/share/setnx?k={key}", data=b"held", timeout=10)

    with ThreadPoolExecutor(max_workers=8) as pool:
        results = [
            f.result().content for f in as_completed(pool.submit(try_lock) for _ in range(8))
        ]

    assert results.count(b"1") == 1
    assert results.count(b"0") == 7


def test_setnx_ttl_reacquire_across_workers():
    key = f"ttl-lock-{time.time_ns()}"
    r1 = requests.post(f"{BASE_URL}/share/setnx?k={key}&ttl=1", data=b"x", timeout=10)
    assert r1.content == b"1"
    r2 = requests.post(f"{BASE_URL}/share/setnx?k={key}", data=b"y", timeout=10)
    assert r2.content == b"0"
    time.sleep(1.2)
    r3 = requests.post(f"{BASE_URL}/share/setnx?k={key}", data=b"z", timeout=10)
    assert r3.content == b"1"


def test_keys_across_workers_and_types():
    prefix = f"it-{time.time_ns()}:"
    requests.post(f"{BASE_URL}/asgi/set?k={prefix}scalar", data=b"v", timeout=10).raise_for_status()
    requests.post(
        f"{BASE_URL}/share/append?k={prefix}list", data=b"a", timeout=10
    ).raise_for_status()
    requests.post(f"{BASE_URL}/share/sadd?k={prefix}set", data=b"m", timeout=10).raise_for_status()
    got = requests.get(f"{BASE_URL}/share/keys?prefix={prefix}", timeout=10)
    assert got.status_code == 200
    names = set(got.json())
    assert f"{prefix}scalar" in names
    assert f"{prefix}list" in names
    assert f"{prefix}set" in names


def test_keys_security_namespace_does_not_leak():
    secret = f"secret-{time.time_ns()}:"
    mine = f"mine-{time.time_ns()}:"
    requests.post(f"{BASE_URL}/asgi/set?k={secret}hidden", data=b"x", timeout=10).raise_for_status()
    requests.post(f"{BASE_URL}/asgi/set?k={mine}visible", data=b"y", timeout=10).raise_for_status()
    got = requests.get(f"{BASE_URL}/share/keys?prefix={mine}", timeout=10)
    names = got.json()
    assert f"{mine}visible" in names
    assert not any(n.startswith(secret) for n in names)


def test_pubsub_cross_worker_delivery():
    ch = f"ch-{time.time_ns()}"
    out: list[bytes] = []

    def sub():
        resp = requests.get(f"{BASE_URL}/share/subscribe?ch={ch}&t=20", timeout=25)
        out.append(resp.content)

    th = threading.Thread(target=sub)
    th.start()
    time.sleep(0.5)
    pub = requests.post(f"{BASE_URL}/share/publish?ch={ch}", data=b"evt", timeout=10)
    assert pub.status_code == 200
    assert int(pub.text) >= 1
    th.join(timeout=25)
    assert out == [b"evt"]


def test_pubsub_fanout_multiple_workers():
    ch = f"fan-{time.time_ns()}"
    results: list[bytes] = []
    lock = threading.Lock()

    def sub():
        resp = requests.get(f"{BASE_URL}/share/subscribe?ch={ch}&t=20", timeout=25)
        with lock:
            results.append(resp.content)

    threads = [threading.Thread(target=sub) for _ in range(2)]
    for t in threads:
        t.start()
    time.sleep(0.5)
    pub = requests.post(f"{BASE_URL}/share/publish?ch={ch}", data=b"fanout", timeout=10)
    assert int(pub.text) == 2
    for t in threads:
        t.join(timeout=25)
    assert sorted(results) == [b"fanout", b"fanout"]


def test_worker_id_unique_and_stable_per_worker():
    ids = []
    for _ in range(20):
        r = requests.get(f"{BASE_URL}/share/worker-id", timeout=10)
        assert r.status_code == 200
        ids.append(int(r.text))
    assert set(ids) == {0, 1}
    r1 = requests.get(f"{BASE_URL}/share/worker-id", timeout=10)
    r2 = requests.get(f"{BASE_URL}/share/worker-id", timeout=10)
    # round-robin may hit different workers; each response is stable for that worker process
    assert r1.text in {"0", "1"}
    assert r2.text in {"0", "1"}


def test_binary_data_through_new_apis():
    key = f"bin-{time.time_ns()}"
    payload = b"\x00\xff\n"
    requests.post(f"{BASE_URL}/share/sadd?k={key}", data=payload, timeout=10).raise_for_status()
    got = requests.get(f"{BASE_URL}/share/smembers?k={key}", timeout=10)
    assert got.json()[0] == payload.decode("latin1")


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
    test_cross_worker_set_add_members()
    test_pubsub_cross_worker_delivery()
    test_worker_id_unique_and_stable_per_worker()
    test_asgi_aget_and_list()
    test_esgi_cache()
    print("simple_cache integration tests passed")
