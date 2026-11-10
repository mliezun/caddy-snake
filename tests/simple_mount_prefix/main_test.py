"""Integration tests for the python-block mount_prefix subdirective."""

from __future__ import annotations

import sys

import requests

BASE = "http://localhost:9080"
DYNAMIC = "http://localhost:9081"


def get(url: str, **kwargs) -> requests.Response:
    return requests.get(url, timeout=30, allow_redirects=False, **kwargs)


def check_wsgi() -> None:
    r = get(f"{BASE}/wsgi/")
    assert r.status_code == 200, r.text
    data = r.json()
    assert data["script_name"] == "/wsgi", data
    assert data["path_info"] == "/", data
    assert data["url_for_item"] == "/wsgi/items/7", data
    assert data["leaked"] == [], data

    r = get(f"{BASE}/wsgi/items/42?q=x")
    assert r.status_code == 200, r.text
    data = r.json()
    assert data == {
        "item_id": 42,
        "script_name": "/wsgi",
        "path_info": "/items/42",
        "query": "x",
    }, data

    # Framework-generated redirects include the prefix.
    r = get(f"{BASE}/wsgi/go")
    assert r.status_code == 302, r.status_code
    assert r.headers["Location"] == "/wsgi/items/1", r.headers["Location"]
    print("  OK  WSGI: SCRIPT_NAME / PATH_INFO / url_for / redirects")


def check_asgi() -> None:
    r = get(f"{BASE}/asgi/")
    assert r.status_code == 200, r.text
    data = r.json()
    assert data["root_path"] == "/asgi", data
    assert data["path"] == "/asgi/", data
    assert data["raw_path"] == "/asgi/", data
    assert data["url_for_item"] == "http://localhost:9080/asgi/items/7", data
    assert data["leaked"] == [], data

    r = get(f"{BASE}/asgi/items/42")
    assert r.status_code == 200, r.text
    assert r.json() == {"item_id": 42, "root_path": "/asgi", "path": "/asgi/items/42"}, r.json()
    print("  OK  ASGI: root_path / full path / url_for")


def check_bare_prefix_redirect() -> None:
    for prefix in ("/wsgi", "/asgi"):
        r = get(f"{BASE}{prefix}?a=1")
        assert r.status_code == 308, (prefix, r.status_code)
        assert r.headers["Location"] == f"{prefix}/?a=1", r.headers["Location"]
    print("  OK  bare prefix redirects to <prefix>/ and keeps the query")


def check_outside_prefix() -> None:
    # The matcher /wsgi* also matches /wsgix, which is not under the mount point.
    r = get(f"{BASE}/wsgix/items/1")
    assert r.status_code == 404, r.status_code
    print("  OK  path outside the prefix returns 404")


def check_spoofed_header() -> None:
    spoof = {"Caddy-Snake-Mount-Prefix": "/evil"}
    r = get(f"{BASE}/wsgi/", headers=spoof)
    assert r.json()["script_name"] == "/wsgi", r.json()
    r = get(f"{BASE}/asgi/", headers=spoof)
    assert r.json()["root_path"] == "/asgi", r.json()
    # A handler without mount_prefix must ignore the header entirely: the app
    # sees the unstripped path, so Flask has no matching route.
    r = get(f"{BASE}/root/items/3", headers={"Caddy-Snake-Mount-Prefix": "/root"})
    assert r.status_code == 404, r.status_code
    print("  OK  client-supplied Caddy-Snake-Mount-Prefix is ignored")


def check_dynamic() -> None:
    for name in ("alpha", "beta"):
        r = get(f"{DYNAMIC}/{name}/reports/2026")
        assert r.status_code == 200, r.text
        assert r.json() == {
            "app": name,
            "script_name": f"/{name}",
            "path_info": "/reports/2026",
        }, r.json()
        r = get(f"{DYNAMIC}/{name}")
        assert r.status_code == 308, r.status_code
        assert r.headers["Location"] == f"/{name}/", r.headers["Location"]
    # gamma is an ASGI app behind the same module_auto handler.
    r = get(f"{DYNAMIC}/gamma/reports/2026")
    assert r.status_code == 200, r.text
    assert r.json() == {
        "app": "gamma",
        "root_path": "/gamma",
        "path": "/gamma/reports/2026",
    }, r.json()
    # A first segment that cannot be a mount point is not served.
    r = get(f"{DYNAMIC}/not%20valid/x")
    assert r.status_code == 404, r.status_code
    print("  OK  placeholder prefix + module_auto serve WSGI and ASGI apps by first path segment")


def main() -> int:
    check_wsgi()
    check_asgi()
    check_bare_prefix_redirect()
    check_outside_prefix()
    check_spoofed_header()
    check_dynamic()
    print("simple_mount_prefix integration tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
