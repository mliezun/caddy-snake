"""Integration tests for X-Forwarded-* handling on the hop to Python workers.

A TLS-terminating proxy (Traefik, nginx, a load balancer) forwards plain HTTP
to Caddy with X-Forwarded-Proto: https. When Caddy trusts that proxy
(servers { trusted_proxies ... }), the WSGI/ASGI app must see the original
scheme, host and client chain. When it does not, client-supplied values must
be dropped. See https://github.com/mliezun/caddy-snake/issues/252.
"""

from __future__ import annotations

import sys
import time

import requests

TRUSTED = "http://127.0.0.1:9080"
UNTRUSTED = "http://127.0.0.1:9081"

PROXY_HEADERS = {
    "X-Forwarded-Proto": "https",
    "X-Forwarded-Host": "app.example.com",
    "X-Forwarded-For": "203.0.113.7",
}


def fetch(base: str, path: str, headers: dict[str, str] | None = None) -> dict:
    resp = requests.get(f"{base}{path}", headers=headers or {}, timeout=30)
    assert resp.status_code == 200, f"{base}{path}: {resp.status_code} {resp.text}"
    return resp.json()


def check(name: str, got: dict, want: dict) -> list[str]:
    errors = []
    for key, expected in want.items():
        if got.get(key) != expected:
            errors.append(f"{name}: {key} = {got.get(key)!r}, want {expected!r}")
    return errors


def wait_ready(timeout: float = 60.0) -> None:
    deadline = time.monotonic() + timeout
    pending = [
        f"{base}/{iface}/info" for base in (TRUSTED, UNTRUSTED) for iface in ("wsgi", "asgi")
    ]
    while pending:
        url = pending[0]
        try:
            resp = requests.get(url, timeout=5)
            if resp.status_code == 200 and resp.content:
                pending.pop(0)
                continue
        except requests.RequestException:
            pass
        if time.monotonic() > deadline:
            raise TimeoutError(f"worker not ready: {url}")
        time.sleep(0.5)


def main() -> int:
    wait_ready()
    errors: list[str] = []
    for iface in ("wsgi", "asgi"):
        path = f"/{iface}/info"

        # Trusted proxy that terminated TLS: keep its forwarded values.
        errors += check(
            f"{iface} trusted proxy",
            fetch(TRUSTED, path, PROXY_HEADERS),
            {
                "scheme": "https",
                "x-forwarded-proto": "https",
                "x-forwarded-host": "app.example.com",
                "x-forwarded-for": "203.0.113.7, 127.0.0.1",
            },
        )

        # Trusted peer without forwarded headers: describe this hop.
        errors += check(
            f"{iface} trusted, no headers",
            fetch(TRUSTED, path),
            {
                "scheme": "http",
                "x-forwarded-proto": "http",
                "x-forwarded-host": "127.0.0.1:9080",
                "x-forwarded-for": "127.0.0.1",
            },
        )

        # Untrusted client spoofing forwarded headers: drop them all.
        errors += check(
            f"{iface} untrusted spoof",
            fetch(UNTRUSTED, path, PROXY_HEADERS),
            {
                "scheme": "http",
                "x-forwarded-proto": "http",
                "x-forwarded-host": "127.0.0.1:9081",
                "x-forwarded-for": "127.0.0.1",
            },
        )

    if errors:
        print("simple_forwarded integration tests FAILED:")
        for e in errors:
            print(f"  FAIL  {e}")
        return 1
    print("simple_forwarded integration tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
