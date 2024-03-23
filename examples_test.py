import time
from concurrent.futures import ThreadPoolExecutor
import requests

def do_requests(app: str, expected_status: int = 200, n: int = 1_000):
    for _ in range(n):
        r = requests.get(f"http://localhost:9080/{app}")
        assert r.status_code == expected_status
    return n

with ThreadPoolExecutor(max_workers=8) as t:
    start = time.time()
    request_count = sum(t.map(do_requests, ["app1", "app2", "app3", "app4"]*3, [200, 200, 500, 200]*3))
    print(f"Done {request_count=}")
    print(f"Elapsed: {time.time()-start}s")
