"""Integration tests for Docker isolation."""

from __future__ import annotations

import os
import subprocess
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


def test_no_leftover_worker_containers_leftover():
    docker_host = os.environ.get("DOCKER_HOST", "")
    ids = subprocess.check_output(
        ["docker", "ps", "-aq", "--filter", "label=caddy-snake.worker=true"],
        text=True,
        env={**os.environ, **({"DOCKER_HOST": docker_host} if docker_host else {})},
    ).strip()
    assert ids == "", f"leftover worker containers: {ids!r}"
