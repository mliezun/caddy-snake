import base64
import os
import time
import uuid
from concurrent.futures import ThreadPoolExecutor

import requests

item_count = 0

BASE_URL = "http://localhost:9080"

BIG_BLOB = base64.b64encode(os.urandom(4 * 2**20)).decode("utf")


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


def upload_file():
    # Only upload every 10th item
    if item_count % 10 != 0:
        return True
    # Open the caddy binary itself
    # This is just to test the upload functionality
    with open("./caddy", "rb") as f:
        response = requests.post(f"{BASE_URL}/item/upload-file/", files={"file": f})
        f.seek(0)
        binary_content = f.read()
        return response.ok and response.content == binary_content


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
    assert upload_file(), "Upload file failed"


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


def test_path_encoding_flask_route_param():
    """Issue #237: Flask route params must match gunicorn (UTF-8 text)."""
    r = requests.get(f"{BASE_URL}/encoding/param/åäö")
    assert r.status_code == 200, r.text
    assert r.text == "åäö, ['0xe5', '0xe4', '0xf6']", r.text


def test_path_encoding_flask_raw_path_info():
    """Issue #237: PATH_INFO is latin-1 of the UTF-8 octets (PEP 3333)."""
    r = requests.get(f"{BASE_URL}/encoding/path_info/åäö")
    assert r.status_code == 200, r.text
    assert r.text == "Ã¥Ã¤Ã¶, ['0xc3', '0xa5', '0xc3', '0xa4', '0xc3', '0xb6']", r.text


def test_path_encoding_flask_cjk():
    r = requests.get(f"{BASE_URL}/encoding/param/日")
    assert r.status_code == 200, r.text
    assert r.text.startswith("日,"), r.text
    r2 = requests.get(f"{BASE_URL}/encoding/path_info/日")
    assert r2.status_code == 200, r2.text
    assert r2.text == "æ\x97¥, ['0xe6', '0x97', '0xa5']", r2.text


if __name__ == "__main__":
    import sys

    test_path_encoding_flask_route_param()
    test_path_encoding_flask_raw_path_info()
    test_path_encoding_flask_cjk()
    print("Path encoding tests passed")

    count = int(sys.argv[1]) if len(sys.argv) > 1 else 2_500
    make_objects(max_workers=4, count=count)
