"""Integration tests for Docker isolation."""

from __future__ import annotations

import os
import subprocess
import time
from pathlib import Path

import requests

BASE_URL = "http://localhost:9080"
SECRET_ENV = "CADDYSNAKE_ISOLATION_PROBE_SECRET"
ALLOWED_ENV = "ISOLATION_ALLOWED"
OUTSIDE_DIR = Path(__file__).resolve().parent.parent / "secret_outside"


def test_hello():
    r = requests.get(f"{BASE_URL}/hello", timeout=30)
    assert r.status_code == 200, r.text
    assert r.content == b"hello-isolated"


def test_env_isolation():
    r = requests.get(f"{BASE_URL}/env/probe", timeout=30)
    assert r.status_code == 200, r.text
    data = r.json()
    assert data["probe_secret"] == "", data
    assert data["allowed"] == "visible", data
    assert os.environ.get(SECRET_ENV, "set-on-host") != ""


def test_filesystem_isolation():
    OUTSIDE_DIR.mkdir(parents=True, exist_ok=True)
    secret_file = OUTSIDE_DIR / "host-secret.txt"
    secret_file.write_text("host-only-secret", encoding="utf-8")
    assert secret_file.read_text(encoding="utf-8") == "host-only-secret"

    r = requests.get(
        f"{BASE_URL}/fs/outside",
        params={"path": str(secret_file)},
        timeout=30,
    )
    assert r.status_code == 403, r.text


def test_runtime_exception_is_relayed_to_caddy_log(log_path: str = "caddy.log"):
    """Docker-isolated workers must expose runtime tracebacks like local workers."""
    with open(log_path, "rb") as fd:
        fd.seek(0, os.SEEK_END)
        log_offset = fd.tell()

    response = requests.get(f"{BASE_URL}/boom", timeout=30)
    assert response.status_code == 500, response.text
    assert b"isolated-intentional-boom" not in response.content
    assert b"Traceback" not in response.content

    deadline = time.time() + 5
    logs = ""
    while time.time() < deadline:
        with open(log_path, encoding="utf-8", errors="replace") as fd:
            fd.seek(log_offset)
            logs = fd.read()
        if "RuntimeError: isolated-intentional-boom" in logs:
            break
        time.sleep(0.1)
    else:
        raise AssertionError(
            "expected isolated worker traceback in caddy.log, got:\n" + logs[-4000:]
        )

    assert "Unhandled exception in WSGI handler" in logs
    assert "GET /boom" in logs
    assert "Traceback (most recent call last)" in logs


def test_no_leftover_worker_containers_leftover():
    docker_host = os.environ.get("DOCKER_HOST", "")
    ids = subprocess.check_output(
        ["docker", "ps", "-aq", "--filter", "label=caddy-snake.worker=true"],
        text=True,
        env={**os.environ, **({"DOCKER_HOST": docker_host} if docker_host else {})},
    ).strip()
    assert ids == "", f"leftover worker containers: {ids!r}"
