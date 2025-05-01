import os
import base64
import uuid
import time
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
    with open("./caddy", "rb") as f:
        response = requests.post(f"{BASE_URL}/item/upload-file/", files={"file": f})
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
    print(f"Elapsed: {time.time()-start}s")


if __name__ == "__main__":
    make_objects(max_workers=4, count=2_500)
