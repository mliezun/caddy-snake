"""End-to-end tests for scalar operations across two cluster-cache nodes."""

from __future__ import annotations

import time

import requests

NODE_A = "http://localhost:9081"
NODE_B = "http://localhost:9082"


def wait_for_nodes():
    deadline = time.monotonic() + 10
    pending = {NODE_A, NODE_B}
    while pending and time.monotonic() < deadline:
        for node in tuple(pending):
            try:
                response = requests.get(f"{node}/get", params={"key": "readiness"}, timeout=1)
                if response.status_code == 200:
                    pending.remove(node)
            except requests.RequestException:
                pass
        if pending:
            time.sleep(0.1)
    assert not pending, f"cluster nodes did not become ready: {sorted(pending)}"


def test_cross_node_scalar_crud():
    key = f"crud-{time.time_ns()}"
    payload = b"cluster\x00value"
    response = requests.post(f"{NODE_A}/set", params={"key": key}, data=payload, timeout=10)
    response.raise_for_status()

    got = requests.get(f"{NODE_B}/get", params={"key": key}, timeout=10)
    got.raise_for_status()
    assert got.content == payload

    deleted = requests.post(f"{NODE_B}/delete", params={"key": key}, timeout=10)
    deleted.raise_for_status()
    assert deleted.content == b"1"
    assert requests.get(f"{NODE_A}/get", params={"key": key}, timeout=10).content == b"miss"


def test_cross_node_ttl():
    key = f"ttl-{time.time_ns()}"
    response = requests.post(
        f"{NODE_B}/set", params={"key": key, "ttl": 1}, data=b"short", timeout=10
    )
    response.raise_for_status()
    assert requests.get(f"{NODE_A}/get", params={"key": key}, timeout=10).content == b"short"
    time.sleep(1.2)
    assert requests.get(f"{NODE_A}/get", params={"key": key}, timeout=10).content == b"miss"


def test_non_scalar_operation_is_rejected():
    response = requests.post(
        f"{NODE_A}/append", params={"key": "unsupported"}, data=b"x", timeout=10
    )
    assert response.status_code == 409
    assert b"supports only CSGET, CSSET, and CSDEL" in response.content


if __name__ == "__main__":
    wait_for_nodes()
    test_cross_node_scalar_crud()
    test_cross_node_ttl()
    test_non_scalar_operation_is_rejected()
    print("simple_cluster_cache integration tests passed")
