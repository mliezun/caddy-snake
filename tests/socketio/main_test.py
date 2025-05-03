import os
import base64
import socketio
import time
from concurrent.futures import ThreadPoolExecutor
import psutil

user_count = 0

BASE_URL = "http://localhost:9080"

BIG_BLOB = base64.b64encode(os.urandom(2**18)).decode("utf")


def get_dummy_user() -> dict:
    global user_count
    user_count += 1
    return {
        "name": f"User {user_count}",
        "description": f"User Description {user_count}",
        "blob": BIG_BLOB if user_count % 4 == 0 else None,
    }


def user_lifecycle():
    sio = socketio.Client()

    connected_ok = False
    disconnected_ok = False
    ping_data = get_dummy_user()
    pong_data = []

    @sio.event
    def connect():
        nonlocal connected_ok
        connected_ok = True

    @sio.event
    def disconnect():
        nonlocal disconnected_ok
        disconnected_ok = True

    @sio.event
    def pong(data):
        nonlocal pong_data
        pong_data.append(data)

    sio.connect(BASE_URL)
    start_data = sio.call("start", ping_data)
    sio.emit("ping", ping_data)

    time.sleep(1 if not ping_data.get("blob") else 5)
    assert connected_ok
    assert start_data in pong_data
    assert start_data["data"] == ping_data

    sio.disconnect()
    time.sleep(1)
    assert disconnected_ok


def make_users(max_workers: int, count: int):
    start = time.time()
    failed = False

    def user_done(fut):
        exc = fut.exception()
        if exc:
            nonlocal failed
            failed = True
            raise SystemExit(1) from exc

    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        for _ in range(count):
            future = executor.submit(user_lifecycle)
            future.add_done_callback(user_done)

    if failed:
        print("Tests failed")
        exit(1)

    print(f"Created and destroyed {count} users")
    print(f"Elapsed: {time.time() - start}s")


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


def check_user_events_on_logs(logs: str, check_count: int = 256):
    events_count = {
        "User connected": 0,
        "User disconnected": 0,
    }
    with open(logs, "r") as fd:
        for line in fd:
            event = line.strip()
            for event_key in events_count.keys():
                if event_key in event:
                    events_count[event_key] += 1
    for event, count in events_count.items():
        assert count == check_count, (
            f"Expected '{event}' to only be seen {check_count}, but seen {count} times"
        )


if __name__ == "__main__":
    import sys

    count = int(sys.argv[1]) if len(sys.argv) > 1 else 256
    make_users(max_workers=8, count=count)
    find_and_terminate_process("caddy")
    check_user_events_on_logs("caddy.log", check_count=count)
