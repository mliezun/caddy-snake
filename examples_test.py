import time
from concurrent.futures import ThreadPoolExecutor
import requests

def do_requests(app: str, n: int = 1_000):
    for _ in range(n):
        r = requests.get(f"http://localhost:9080/{app}")
        r.raise_for_status()
    return n

with ThreadPoolExecutor(max_workers=8) as t:
    start = time.time()
    request_count = sum(t.map(do_requests, ["app1", "app2"]*4))
    print(f"Done {request_count=}")
    print(f"Elapsed: {time.time()-start}s")
