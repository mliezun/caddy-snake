"""Integration tests for dynamic module loading with Caddy placeholders.

Each subdomain (app1, app2, app3) is routed to a separate Python ASGI app
determined at request time by resolving {http.request.host.labels.6} in both
the module name and the working directory.
"""

import time
from concurrent.futures import ThreadPoolExecutor

import requests

BASE_URL = "http://127.0.0.1:9080"

APPS = {
    "app1": "Hello from app1",
    "app2": "Hello from app2",
    "app3": "Hello from app3",
}


def request_app(name: str) -> requests.Response:
    """Send a GET request to the given app subdomain via Host header."""
    host = f"{name}.127.0.0.1.nip.io"
    return requests.get(f"{BASE_URL}/hello", headers={"Host": host})


def test_individual_apps():
    """Each app should return its own unique greeting."""
    for name, expected_body in APPS.items():
        resp = request_app(name)
        assert resp.status_code == 200, (
            f"[{name}] expected status 200, got {resp.status_code}"
        )
        assert resp.text == expected_body, (
            f"[{name}] expected body '{expected_body}', got '{resp.text}'"
        )
        print(f"  OK  {name} -> {resp.text!r}")


def test_cached_apps():
    """Second round of requests should hit cached app instances."""
    for name, expected_body in APPS.items():
        resp = request_app(name)
        assert resp.status_code == 200, (
            f"[{name} cached] expected status 200, got {resp.status_code}"
        )
        assert resp.text == expected_body, (
            f"[{name} cached] expected body '{expected_body}', got '{resp.text}'"
        )
        print(f"  OK  {name} (cached) -> {resp.text!r}")


def test_concurrent_requests():
    """Fire concurrent requests to all three apps and verify responses."""
    failed = False

    def check_app(name, expected_body):
        nonlocal failed
        resp = request_app(name)
        if resp.status_code != 200 or resp.text != expected_body:
            failed = True
            raise AssertionError(
                f"[{name} concurrent] status={resp.status_code} body={resp.text!r}"
            )

    with ThreadPoolExecutor(max_workers=6) as pool:
        futures = []
        for _ in range(30):
            for name, expected_body in APPS.items():
                futures.append(pool.submit(check_app, name, expected_body))
        for f in futures:
            f.result()

    assert not failed, "Some concurrent requests failed"
    print(f"  OK  {len(APPS) * 30} concurrent requests passed")


if __name__ == "__main__":
    start = time.time()

    print(">>> Testing individual apps...")
    test_individual_apps()

    print(">>> Testing cached apps...")
    test_cached_apps()

    print(">>> Testing concurrent requests...")
    test_concurrent_requests()

    elapsed = time.time() - start
    print(f"\nAll dynamic module tests passed! ({elapsed:.2f}s)")
