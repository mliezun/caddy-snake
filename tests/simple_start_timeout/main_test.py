"""Integration tests for python start_timeout across WSGI/ASGI/ESGI/dynamic.

Cases per mode:
  - timeout: worker delay exceeds start_timeout → readiness failure
  - ok: worker starts within start_timeout → serves traffic
  - warning: start_timeout > warn-after and delay > warn-after → warn then succeed

CADDYSNAKE_START_TIMEOUT_WARN_AFTER is set to 1s so the warning case stays fast.
"""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import requests

ROOT = Path(__file__).resolve().parent
CADDY_BIN = ROOT / "caddy"
WARN_AFTER = "1s"
WARN_AFTER_SEC = 1.0

MODES = ("wsgi", "asgi", "esgi", "dynamic")

EXPECTED_BODY = {
    "wsgi": b"ok-wsgi",
    "asgi": b"ok-asgi",
    "esgi": b"ok-esgi",
    "dynamic": b"ok-asgi",
}


def module_directive(mode: str) -> str:
    if mode == "wsgi":
        return 'module_wsgi "app:wsgi_app"'
    if mode == "asgi":
        return 'module_asgi "app:asgi_app"'
    if mode == "esgi":
        return 'module_esgi "app:application"\n\t\truntime gevent'
    if mode == "dynamic":
        return 'module_asgi "app:asgi_app"'
    raise ValueError(mode)


def site_address(mode: str, port: int) -> str:
    if mode == "dynamic":
        return f"app1.127.0.0.1.nip.io:{port}"
    return f":{port}"


def request_url(mode: str, port: int) -> str:
    return f"http://127.0.0.1:{port}/"


def request_headers(mode: str) -> dict[str, str]:
    if mode == "dynamic":
        return {"Host": "app1.127.0.0.1.nip.io"}
    return {}


def working_dir_line(mode: str) -> str:
    if mode == "dynamic":
        return 'working_dir "tenants/{http.request.host.labels.6}"'
    return 'working_dir "."'


def write_caddyfile(path: Path, *, mode: str, port: int, start_timeout: str, delay: str) -> None:
    content = f"""{{
	http_port {port}
	https_port {port + 1}
	auto_https off
	log {{
		level info
	}}
}}
{site_address(mode, port)} {{
	python /* {{
		{module_directive(mode)}
		{working_dir_line(mode)}
		venv "./venv"
		workers 1
		start_timeout {start_timeout}
		env_var START_DELAY "{delay}"
	}}
}}
"""
    path.write_text(content, encoding="utf-8")


def caddy_env() -> dict[str, str]:
    env = os.environ.copy()
    env["CADDYSNAKE_START_TIMEOUT_WARN_AFTER"] = WARN_AFTER
    return env


def start_caddy(caddyfile: Path, log_path: Path) -> subprocess.Popen:
    # Close the parent fd after spawn; the child keeps its inherited copy.
    with open(log_path, "w", encoding="utf-8") as log_f:
        return subprocess.Popen(
            [str(CADDY_BIN), "run", "--config", str(caddyfile)],
            cwd=str(ROOT),
            stdout=log_f,
            stderr=subprocess.STDOUT,
            env=caddy_env(),
            start_new_session=True,
        )


def stop_caddy(proc: subprocess.Popen | None) -> None:
    if proc is None:
        return
    try:
        if proc.poll() is None:
            os.killpg(proc.pid, signal.SIGTERM)
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL)
                proc.wait(timeout=5)
    except ProcessLookupError:
        pass


def read_log(log_path: Path) -> str:
    try:
        return log_path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""


def wait_for_log(log_path: Path, needle: str, timeout: float) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if needle in read_log(log_path):
            return True
        time.sleep(0.05)
    return False


def wait_for_exit(proc: subprocess.Popen, timeout: float) -> int | None:
    try:
        return proc.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        return None


def assert_http_ok(mode: str, port: int, timeout: float = 10.0) -> None:
    url = request_url(mode, port)
    headers = request_headers(mode)
    deadline = time.time() + timeout
    last_err: Exception | None = None
    while time.time() < deadline:
        try:
            resp = requests.get(url, headers=headers, timeout=2)
            if resp.status_code == 200 and resp.content == EXPECTED_BODY[mode]:
                return
            last_err = AssertionError(f"status={resp.status_code} body={resp.content!r}")
        except requests.RequestException as exc:
            last_err = exc
        time.sleep(0.1)
    raise AssertionError(f"HTTP ok failed for {mode}:{port}: {last_err}")


def run_timeout_case(mode: str, port: int) -> None:
    """Delay 2s with start_timeout 1s → readiness failure."""
    with tempfile.TemporaryDirectory(prefix="sst-timeout-") as tmp:
        tmp_path = Path(tmp)
        caddyfile = tmp_path / "Caddyfile"
        log_path = tmp_path / "caddy.log"
        write_caddyfile(caddyfile, mode=mode, port=port, start_timeout="1s", delay="2")

        proc = start_caddy(caddyfile, log_path)
        try:
            if mode == "dynamic":
                # Dynamic workers start on first request; Caddy itself should come up.
                assert wait_for_log(log_path, "finished cleaning storage units", 30), (
                    f"[{mode}/timeout] caddy failed to start:\n{read_log(log_path)}"
                )
                resp = requests.get(
                    request_url(mode, port),
                    headers=request_headers(mode),
                    timeout=10,
                )
                assert resp.status_code >= 500, (
                    f"[{mode}/timeout] expected 5xx, got {resp.status_code} body={resp.content!r}"
                )
                assert wait_for_log(log_path, "not ready within", 5) or wait_for_log(
                    log_path, "waiting for Python worker", 1
                ), f"[{mode}/timeout] missing readiness error:\n{read_log(log_path)}"
            else:
                code = wait_for_exit(proc, timeout=15)
                assert code is not None and code != 0, (
                    f"[{mode}/timeout] expected caddy to exit non-zero, got {code}\n{read_log(log_path)}"
                )
                log = read_log(log_path)
                assert "not ready within" in log or "waiting for Python worker" in log, (
                    f"[{mode}/timeout] missing readiness error:\n{log}"
                )
            print(f"  OK  {mode}/timeout")
        finally:
            stop_caddy(proc)


def run_ok_case(mode: str, port: int) -> None:
    """No artificial delay; default-ish short start_timeout → success."""
    with tempfile.TemporaryDirectory(prefix="sst-ok-") as tmp:
        tmp_path = Path(tmp)
        caddyfile = tmp_path / "Caddyfile"
        log_path = tmp_path / "caddy.log"
        write_caddyfile(caddyfile, mode=mode, port=port, start_timeout="10s", delay="0")

        proc = start_caddy(caddyfile, log_path)
        try:
            assert wait_for_log(log_path, "finished cleaning storage units", 30), (
                f"[{mode}/ok] caddy failed to start:\n{read_log(log_path)}"
            )
            assert_http_ok(mode, port)
            log = read_log(log_path)
            assert "taking a long time to load" not in log, (
                f"[{mode}/ok] unexpected slow-start warning:\n{log}"
            )
            print(f"  OK  {mode}/ok")
        finally:
            stop_caddy(proc)


def run_warning_case(mode: str, port: int) -> None:
    """Delay past warn-after but within start_timeout → warning then success."""
    with tempfile.TemporaryDirectory(prefix="sst-warn-") as tmp:
        tmp_path = Path(tmp)
        caddyfile = tmp_path / "Caddyfile"
        log_path = tmp_path / "caddy.log"
        # warn-after=1s (env), delay=1.5s, start_timeout=5s
        write_caddyfile(caddyfile, mode=mode, port=port, start_timeout="5s", delay="1.5")

        proc = start_caddy(caddyfile, log_path)
        try:
            if mode == "dynamic":
                assert wait_for_log(log_path, "finished cleaning storage units", 30), (
                    f"[{mode}/warning] caddy failed to start:\n{read_log(log_path)}"
                )
                # First request triggers worker start (and the warning).
                assert_http_ok(mode, port, timeout=15)
            else:
                # Static modes block provision until ready; expect warning then ready.
                assert wait_for_log(log_path, "taking a long time to load", WARN_AFTER_SEC + 3), (
                    f"[{mode}/warning] missing slow-start warning:\n{read_log(log_path)}"
                )
                assert wait_for_log(log_path, "finished cleaning storage units", 15), (
                    f"[{mode}/warning] caddy failed to become ready:\n{read_log(log_path)}"
                )
                assert_http_ok(mode, port)

            log = read_log(log_path)
            assert "taking a long time to load" in log, (
                f"[{mode}/warning] missing slow-start warning:\n{log}"
            )
            print(f"  OK  {mode}/warning")
        finally:
            stop_caddy(proc)


def main() -> int:
    if not CADDY_BIN.is_file():
        print(f"missing caddy binary at {CADDY_BIN}", file=sys.stderr)
        return 1

    # Unique ports per scenario to avoid TIME_WAIT clashes between cases.
    base = 19080
    idx = 0
    for mode in MODES:
        print(f"== mode {mode} ==")
        run_timeout_case(mode, base + idx)
        idx += 2
        run_ok_case(mode, base + idx)
        idx += 2
        run_warning_case(mode, base + idx)
        idx += 2

    print("simple_start_timeout integration tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
