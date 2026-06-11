# Benchmarks

Compares Caddy Snake performance against traditional reverse proxy setups for **Flask (WSGI)**, **FastAPI (ASGI)**, and **ESGI (gevent)** on the same JSON `GET /hello` workload.

## What's tested

| Configuration | Description |
|---|---|
| Flask + Gunicorn + Caddy | Gunicorn (1 worker, 4 threads) behind Caddy reverse proxy |
| Flask + Caddy Snake | Flask served directly by Caddy via caddy-snake (process worker) |
| FastAPI + Uvicorn + Caddy | Uvicorn (1 worker) behind Caddy reverse proxy |
| FastAPI + Caddy Snake | FastAPI served directly by Caddy via caddy-snake (process worker) |
| ESGI (gevent) + Caddy reverse proxy | `python caddysnake.py --interface esgi` (gevent `StreamServer` on a Unix socket) behind Caddy `reverse_proxy unix//…` |
| ESGI + Caddy Snake | Same ESGI app via `module_esgi` / `runtime gevent` in the plugin |

The ESGI “reverse proxy” row uses the **same** [`caddysnake.py`](https://github.com/mliezun/caddy-snake/blob/main/caddysnake.py) HTTP/ESGI gateway as worker subprocesses—only the wiring differs (HTTP to Unix socket through Caddy versus the embedded Go path).

## How to run

```bash
# From the repository root
docker build -t caddy-snake-bench -f benchmarks/Dockerfile .
docker run --rm -v $(pwd)/benchmarks:/workspace/benchmarks caddy-snake-bench
```

Results are saved to `benchmarks/results.json` and charts are generated at `benchmarks/benchmark_chart.png` and `benchmarks/benchmark_chart.svg`.

### On Scaleway (POP2-2C-8G, linux/amd64)

For numbers comparable to the historical **Scaleway POP2-2C-8G** setup (and to avoid laptop noise), use the Scaleway CLI to create an instance, run the same Docker harness remotely, pull `results.json` + charts back, and delete the instance:

```bash
# From the repository root — uses your configured `scw` profile (see `scw init`).
./benchmarks/scaleway_bench.sh

# Benchmark this checkout (including uncommitted changes) instead of cloning main:
BENCH_RSYNC_LOCAL=1 ./benchmarks/scaleway_bench.sh
```

Requirements: `scw`, `jq`, `tar`, `ssh`, `nc`; a **public SSH key** in Scaleway IAM matching a key on this machine. Set **`BENCH_SSH_IDENTITY`** to your private key path if it is not auto-detected (example: `~/.ssh/id_ed25519_scw`). The script uses direct `ssh root@$IP` with a disposable known_hosts file. The instance and elastic IP are destroyed on exit unless `KEEP_SERVER=1`.

Example:

```bash
BENCH_RSYNC_LOCAL=1 BENCH_SSH_IDENTITY="$HOME/.ssh/id_ed25519_scw" ./benchmarks/scaleway_bench.sh
```

Environment variables are documented in the script header (`SCW_ZONE`, `BENCH_SERVER_TYPE`, `BENCH_GIT_URL`, `BENCH_GIT_REF`, etc.).

After a successful run, copy the chart into the docs site if you publish updated figures:

```bash
cp benchmarks/benchmark_chart.svg docs/static/img/benchmark_chart.svg
```

## Methodology

- Tool: [hey](https://github.com/rakyll/hey)
- 100 concurrent connections
- 10 second duration per test
- Warmup: 200 requests at 10 concurrency before each test
- 10 runs per configuration, averages in `results.json`
- Docker: Ubuntu 22.04 (see `Dockerfile`). Published table: **Scaleway POP2-2C-8G (linux/amd64)** via `./benchmarks/scaleway_bench.sh`; local Docker follows your host/backend (e.g. arm64 on Apple Silicon).
- Python 3.13, Go 1.26
- Caddy Snake Caddyfiles use `workers 1`; Gunicorn uses 1 worker with 4 threads; Uvicorn uses 1 worker

## Results (4 vCPU / 16 GB cloud VM, linux/amd64 — from `results.json`)

| Configuration | Requests/sec | Avg Latency (ms) | P99 Latency (ms) |
|---|---|---|---|
| Flask + Gunicorn + Caddy | 3,052 | 32.70 | 37.68 |
| **Flask + Caddy Snake** | **4,878** | **20.49** | **28.25** |
| FastAPI + Uvicorn + Caddy | 11,502 | 8.70 | 91.46 |
| **FastAPI + Caddy Snake** | **17,423** | **5.72** | **8.85** |
| ESGI (gevent) + Caddy reverse proxy | 29,193 | 3.43 | 9.97 |
| **ESGI + Caddy Snake** | **34,077** | **2.95** | **8.10** |

Re-run the Docker harness or `./benchmarks/scaleway_bench.sh` to refresh. Other CPUs will differ.
