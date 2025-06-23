import os
import base64
import uuid
import time
import json
import asyncio
import websockets
from concurrent.futures import ThreadPoolExecutor

item_count = 0

BASE_URL = "ws://localhost:9080"
WS_ENDPOINT = f"{BASE_URL}/ws/items/"

# 512KB
BIG_BLOB = base64.b64encode(os.urandom(2**19)).decode("utf")


def get_dummy_item() -> dict:
    global item_count
    item_count += 1
    return {
        "name": f"Item {item_count}",
        "description": f"Item Description {item_count}",
        "blob": BIG_BLOB if item_count % 4 == 0 else None,
    }


async def websocket_store_item(websocket, item_id: str, item: dict) -> bool:
    """Store an item via WebSocket"""
    message = {"action": "store", "item_id": item_id, "item": item}
    await websocket.send(json.dumps(message))
    response = await websocket.recv()
    data = json.loads(response)
    return data.get("status") == "success" and data.get("message") == "Stored"


async def websocket_get_item(websocket, item_id: str, expected_item: dict) -> bool:
    """Retrieve an item via WebSocket"""
    message = {"action": "retrieve", "item_id": item_id}
    await websocket.send(json.dumps(message))
    response = await websocket.recv()
    data = json.loads(response)
    return data.get("status") == "success" and data.get("item") == expected_item


async def websocket_delete_item(websocket, item_id: str) -> bool:
    """Delete an item via WebSocket"""
    message = {"action": "delete", "item_id": item_id}
    await websocket.send(json.dumps(message))
    response = await websocket.recv()
    data = json.loads(response)
    return data.get("status") == "success" and data.get("message") == "Deleted"


async def websocket_ping_test(websocket, test_data: dict) -> bool:
    """Test ping/pong with item data"""
    message = {"action": "ping", "data": test_data}
    await websocket.send(json.dumps(message))
    response = await websocket.recv()
    data = json.loads(response)
    return data.get("action") == "pong" and data.get("data") == test_data


async def item_lifecycle():
    """Test the full lifecycle of an item via WebSocket"""
    item_id = str(uuid.uuid4())
    item = get_dummy_item()

    try:
        async with websockets.connect(WS_ENDPOINT) as websocket:
            # Store item
            assert await websocket_store_item(websocket, item_id, item), (
                "Store item failed"
            )

            # Retrieve item
            assert await websocket_get_item(websocket, item_id, item), "Get item failed"

            # Test ping/pong with item data
            assert await websocket_ping_test(websocket, item), "Ping/pong test failed"

            # Delete item
            assert await websocket_delete_item(websocket, item_id), "Delete item failed"

            # Try to delete again (should fail)
            delete_again_result = await websocket_delete_item(websocket, item_id)
            assert not delete_again_result, "Delete item should fail the second time"

    except Exception as e:
        print(f"WebSocket lifecycle test failed: {e}")
        raise


def run_async_test():
    """Wrapper to run async test in sync context"""
    loop = asyncio.new_event_loop()
    asyncio.set_event_loop(loop)
    try:
        loop.run_until_complete(item_lifecycle())
    finally:
        loop.close()


def make_objects(max_workers: int, count: int):
    start = time.time()
    failed = False

    def item_done(fut):
        exc = fut.exception()
        if exc:
            nonlocal failed
            failed = True
            print(f"Test failed with exception: {exc}")
            raise SystemExit(1) from exc

    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        for _ in range(count):
            future = executor.submit(run_async_test)
            future.add_done_callback(item_done)

    if failed:
        print("Tests failed")
        exit(1)

    print(f"Created and destroyed {count} objects via WebSocket")
    print(f"Elapsed: {time.time() - start}s")


if __name__ == "__main__":
    import sys

    count = (
        int(sys.argv[1]) if len(sys.argv) > 1 else 2500
    )  # Reduced default for WebSocket tests
    make_objects(max_workers=4, count=count)
