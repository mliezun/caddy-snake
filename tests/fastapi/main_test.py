import os
import base64
import uuid
import time
from concurrent.futures import ThreadPoolExecutor
import psutil
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
    print(f"Elapsed: {time.time()-start}s")


def find_and_terminate_process(process_name):
    for proc in psutil.process_iter(["pid", "name"]):
        try:
            if process_name in proc.info["name"]:
                pid = proc.info["pid"]
                p = psutil.Process(pid)
                p.terminate()
                print(f"Process {process_name} with PID {pid} terminated.")
        except (psutil.NoSuchProcess, psutil.AccessDenied, psutil.ZombieProcess):
            pass


def check_lifespan_events_on_logs(logs: str):
    events_count = {
        "Lifespan startup": 0,
        "Lifespan shutdown": 0,
    }
    with open(logs, "r") as fd:
        for line in fd:
            event = line.strip()
            if event in events_count:
                events_count[event] += 1
    for event, count in events_count.items():
        assert (
            count == 1
        ), f"Expected '{event}' to only be seen once, but seen {count} times"


if __name__ == "__main__":
    make_objects(max_workers=4, count=2_500)
    find_and_terminate_process("caddy")
    check_lifespan_events_on_logs("caddy.log")
