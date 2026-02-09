import os
import base64
import uuid
import time
from concurrent.futures import ThreadPoolExecutor
import requests

item_count = 0

BASE_URL = "http://localhost:9080"

BIG_BLOB = base64.b64encode(os.urandom(4 * 2**20)).decode("utf")

MAIN_PY_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "main.py")

# Number of autoreload cycles to test
AUTORELOAD_CYCLES = 5

# Max seconds to wait for each reload to take effect
AUTORELOAD_TIMEOUT = 30


def get_dummy_item() -> dict:
    global item_count
    item_count += 1
    return {
        "name": f"Item {item_count}",
        "description": f"Item Description {item_count}",
        "blob": BIG_BLOB if item_count % 4 == 0 else None,
    }


def store_item(id: str, item: dict):
    response = requests.post(f"{BASE_URL}/item/{id}", json=item)
    return response.status_code == 200 and b"Stored" in response.content


def get_item(id: str, item: dict):
    response = requests.get(f"{BASE_URL}/item/{id}")
    return response.status_code == 200 and response.json() == item


def delete_item(id: str):
    response = requests.delete(f"{BASE_URL}/item/{id}")
    return response.status_code == 200 and b"Deleted" in response.content


def item_lifecycle():
    id = str(uuid.uuid4())
    item = get_dummy_item()
    assert store_item(id, item), "Store item failed"
    assert get_item(id, item), "Get item failed"
    assert delete_item(id), "Delete item failed"
    assert not delete_item(id), "Delete item should fail"


def make_objects(max_workers: int, count: int):
    start = time.time()
    failed = False

    def item_done(fut):
        exc = fut.exception()
        if exc:
            nonlocal failed
            failed = True
            raise SystemExit(1) from exc

    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        for _ in range(count):
            future = executor.submit(item_lifecycle)
            future.add_done_callback(item_done)

    if failed:
        print("Tests failed")
        exit(1)

    print(f"Created and destroyed {count} objects")
    print(f"Elapsed: {time.time() - start}s")


def get_version() -> str:
    """Fetch the current version from the running app."""
    resp = requests.get(f"{BASE_URL}/version", timeout=5)
    resp.raise_for_status()
    return resp.text


def wait_for_version(
    expected: str,
    timeout: float = AUTORELOAD_TIMEOUT,
    retouch_path: str | None = None,
) -> float:
    """Poll /version until it returns `expected` or timeout is reached.

    If *retouch_path* is given and the version hasn't changed after
    RETOUCH_INTERVAL seconds, the file is "touched" (re-written with the same
    content) to re-trigger the filesystem watcher.  This guards against rare
    cases where the inotify/kqueue event is lost (observed in CI).

    Returns the elapsed time in seconds.  Raises AssertionError on timeout.
    """
    RETOUCH_INTERVAL = 5  # seconds between re-touch attempts
    start = time.time()
    deadline = start + timeout
    last_value = None
    last_retouch = start
    while time.time() < deadline:
        try:
            last_value = get_version()
            if last_value == expected:
                return time.time() - start
        except requests.exceptions.RequestException:
            # App might be mid-reload; keep polling
            pass

        # If we've been waiting a while and the version is stuck, re-touch
        # the file so the filesystem watcher fires again.
        now = time.time()
        if retouch_path and (now - last_retouch) >= RETOUCH_INTERVAL:
            try:
                _touch_file(retouch_path)
            except OSError:
                pass
            last_retouch = now

        time.sleep(0.3)
    raise AssertionError(
        f"Timeout after {timeout}s waiting for version '{expected}' "
        f"(last seen: '{last_value}')"
    )


def _write_and_sync(path: str, content: str) -> None:
    """Write *content* to *path* and fsync to ensure the OS flushes the data.

    This makes inotify / kqueue events fire reliably, even under heavy I/O
    (e.g. CI runners).
    """
    fd = os.open(path, os.O_WRONLY | os.O_TRUNC | os.O_CREAT, 0o644)
    try:
        os.write(fd, content.encode())
        os.fsync(fd)
    finally:
        os.close(fd)


def _touch_file(path: str) -> None:
    """Re-write the file with its current content to re-trigger fs watchers."""
    with open(path, "r") as f:
        content = f.read()
    _write_and_sync(path, content)


def rewrite_version(content: str, old_version: str, new_version: str) -> str:
    """Replace the APP_VERSION line in the file content."""
    marker_old = f'APP_VERSION = "{old_version}"'
    marker_new = f'APP_VERSION = "{new_version}"'
    assert marker_old in content, f"Marker '{marker_old}' not found in main.py"
    return content.replace(marker_old, marker_new)


def test_autoreload():
    """Modify main.py several times and verify the running app picks up each change."""

    print(f"\n=== Autoreload test ({AUTORELOAD_CYCLES} cycles) ===")

    # Read the original file so we can restore it no matter what
    with open(MAIN_PY_PATH, "r") as f:
        original_content = f.read()

    current_content = original_content

    try:
        # 1. Verify the initial version
        version = get_version()
        assert version == "v0", f"Expected initial version 'v0', got '{version}'"
        print(f"  Initial version: {version}")

        # 2. Cycle through versions v1 .. v{AUTORELOAD_CYCLES}
        for i in range(1, AUTORELOAD_CYCLES + 1):
            old_ver = f"v{i - 1}"
            new_ver = f"v{i}"

            # Rewrite main.py with the new version (fsync to ensure event fires)
            current_content = rewrite_version(current_content, old_ver, new_ver)
            _write_and_sync(MAIN_PY_PATH, current_content)

            # Wait for the reload to take effect; re-touch file if stuck
            elapsed = wait_for_version(new_ver, retouch_path=MAIN_PY_PATH)
            print(f"  Reload {i}: {old_ver} -> {new_ver}  ({elapsed:.2f}s)")

        print("=== Autoreload test passed ===\n")

    finally:
        # Always restore the original file so the test is idempotent
        _write_and_sync(MAIN_PY_PATH, original_content)
        print("  (main.py restored to original)")


if __name__ == "__main__":
    import sys

    count = int(sys.argv[1]) if len(sys.argv) > 1 else 2_500
    make_objects(max_workers=4, count=count)

    test_autoreload()
