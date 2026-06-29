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

ENV_EXPECTATIONS = {
    "app1": {
        "APP_TEST_FROM_FILE": "app1_file",
        "APP_TEST_INLINE": "app1_inline",
        "APP_TEST_OVERRIDE": "override_app1",
    },
    "app2": {
        "APP_TEST_FROM_FILE": "app2_file",
        "APP_TEST_INLINE": "app2_inline",
        "APP_TEST_OVERRIDE": "override_app2",
    },
    "app3": {
        "APP_TEST_FROM_FILE": "app3_file",
        "APP_TEST_INLINE": "app3_inline",
        "APP_TEST_OVERRIDE": "override_app3",
    },
}


def wait_for_all_apps_healthy(timeout_seconds: float = 90.0, interval: float = 0.5) -> None:
    """Block until every app returns 200 — workers can start after Caddy's storage log line."""

    deadline = time.time() + timeout_seconds
    last: dict[str, str] = {}
    while time.time() < deadline:
        ok = True
        for name, expected_body in APPS.items():
            resp = request_app(name)
            if resp.status_code != 200 or resp.text != expected_body:
                ok = False
                body_preview = (resp.text or "")[:120]
                last[name] = f"status={resp.status_code} body={body_preview!r}"
                break
        if ok:
            return
        time.sleep(interval)

    raise AssertionError(f"Dynamic apps not healthy after {timeout_seconds}s ({last=!r})")


def request_app(name: str) -> requests.Response:
    """Send a GET request to the given app subdomain via Host header."""
    host = f"{name}.127.0.0.1.nip.io"
    return requests.get(f"{BASE_URL}/hello", headers={"Host": host})


def request_app_env(name: str, var: str) -> requests.Response:
    host = f"{name}.127.0.0.1.nip.io"
    return requests.get(f"{BASE_URL}/env/{var}", headers={"Host": host})


def test_individual_apps():
    """Each app should return its own unique greeting."""
    for name, expected_body in APPS.items():
        resp = request_app(name)
        assert resp.status_code == 200, f"[{name}] expected status 200, got {resp.status_code}"
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


def test_env_file_and_env_var():
    """Verify env_file and env_var are applied per dynamic app instance."""
    for app_name, expected in ENV_EXPECTATIONS.items():
        for var, want in expected.items():
            resp = request_app_env(app_name, var)
            assert resp.status_code == 200, (
                f"[{app_name}] GET /env/{var} status={resp.status_code} body={resp.text!r}"
            )
            assert resp.text == want, (
                f"[{app_name}] /env/{var} expected {want!r}, got {resp.text!r}"
            )
            print(f"  OK  {app_name} {var}={resp.text!r}")

        unknown = request_app_env(app_name, "UNKNOWN")
        assert unknown.status_code == 404, (
            f"[{app_name}] expected 404 for unknown env var, got {unknown.status_code}"
        )


if __name__ == "__main__":
    start = time.time()

    print(">>> Waiting for all dynamic apps...")
    wait_for_all_apps_healthy()

    print(">>> Testing individual apps...")
    test_individual_apps()

    print(">>> Testing cached apps...")
    test_cached_apps()

    print(">>> Testing concurrent requests...")
    test_concurrent_requests()

    print(">>> Testing env_file and env_var...")
    test_env_file_and_env_var()

    elapsed = time.time() - start
    print(f"\nAll dynamic module tests passed! ({elapsed:.2f}s)")
