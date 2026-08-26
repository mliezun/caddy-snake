"""Integration tests for python-block request_body without a wrapping route."""

from __future__ import annotations

import sys

import requests

BASE_URL = "http://localhost:9080"
MAX_SIZE = 1024  # 1KiB


def post_zeros(path: str, size: int) -> requests.Response:
    return requests.post(
        f"{BASE_URL}{path}",
        data=b"\x00" * size,
        headers={"Content-Type": "application/octet-stream"},
        timeout=30,
    )


def assert_under_and_over_limit(path: str) -> None:
    under = post_zeros(path, 512)
    assert under.status_code == 200, under.text
    assert under.content == b"512", under.content

    exact = post_zeros(path, MAX_SIZE)
    assert exact.status_code == 200, exact.text
    assert exact.content == b"1024", exact.content

    over = post_zeros(path, MAX_SIZE + 1)
    assert over.status_code == 413, (
        f"{path}: expected 413 for {MAX_SIZE + 1} bytes, got {over.status_code} body={over.content!r}"
    )

    follow = post_zeros(path, 64)
    assert follow.status_code == 200, follow.text
    assert follow.content == b"64", follow.content


def main() -> int:
    assert_under_and_over_limit("/block/upload")
    print("  OK  request_body { max_size 1KiB } without wrapping route")
    assert_under_and_over_limit("/shorthand/upload")
    print("  OK  request_body 1KiB shorthand without wrapping route")
    print("simple_request_body integration tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
