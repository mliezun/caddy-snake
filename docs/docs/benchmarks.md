---
sidebar_position: 7
---

# Benchmarks

Caddy Snake runs Python in worker subprocesses managed by the plugin over a Unix domain socket, avoiding a separately configured Gunicorn or Uvicorn server and the extra HTTP hop of a traditional reverse-proxy setup. The harness compares **Flask (WSGI)**, **FastAPI (ASGI)**, and **ESGI (gevent)** on a minimal JSON `GET /hello` endpoint. The table below matches the committed [`benchmarks/results.json`](https://github.com/mliezun/caddy-snake/blob/main/benchmarks/results.json), produced on a **4 vCPU / 16 GB cloud VM (linux/amd64)** via the Docker harness (`benchmarks/Dockerfile`).

## Test configurations

| Configuration | Description |
|---|---|
| Flask + Gunicorn + Caddy | Gunicorn (1 worker, 4 threads) behind Caddy reverse proxy |
| Flask + Caddy Snake | Flask served directly by Caddy via caddy-snake (process worker) |
| FastAPI + Uvicorn + Caddy | Uvicorn (1 worker) behind Caddy reverse proxy |
| FastAPI + Caddy Snake | FastAPI served directly by Caddy via caddy-snake (process worker) |
| ESGI (gevent) + Caddy reverse proxy | The same [`caddysnake.py`](https://github.com/mliezun/caddy-snake/blob/main/caddysnake.py) gevent ESGI `StreamServer` used by workers, listening on a Unix domain socket with Caddy proxying HTTP to that socket |
| ESGI + Caddy Snake | The ESGI app served via `module_esgi` / `runtime gevent` inside the plugin |

All configurations return the same JSON body: `{"message":"Hello, World!"}`.

## Results

![Benchmark Chart](../static/img/benchmark_chart.svg)

| Configuration | Requests/sec | Avg Latency (ms) | P99 Latency (ms) |
|---|---|---|---|
| Flask + Gunicorn + Caddy | 3,052 | 32.70 | 37.68 |
| **Flask + Caddy Snake** | **4,878** | **20.49** | **28.25** |
| FastAPI + Uvicorn + Caddy | 11,502 | 8.70 | 91.46 |
| **FastAPI + Caddy Snake** | **17,423** | **5.72** | **8.85** |
| ESGI (gevent) + Caddy reverse proxy | 29,193 | 3.43 | 9.97 |
| **ESGI + Caddy Snake** | **34,077** | **2.95** | **8.10** |

Highlights on this hardware:

- **Flask:** Caddy Snake serves ~60% more requests/sec than Gunicorn behind Caddy, with ~25% lower P99 latency.
- **FastAPI:** Caddy Snake serves ~51% more requests/sec than Uvicorn behind Caddy. The P99 gap is the most striking: **8.85 ms vs 91.46 ms** — the embedded path avoids the upstream TCP hop and Uvicorn's accept-queue jitter under 100-connection load.
- **ESGI:** the pair is closest because both rows run the same gevent gateway code from `caddysnake.py`; the embedded wiring is still ~17% faster than proxying HTTP over a Unix socket.

## Methodology

- **Tool:** [hey](https://github.com/rakyll/hey)
- **Concurrency:** 100 connections
- **Duration:** 10 seconds per test run
- **Warmup:** 200 requests at 10 concurrency before each configuration
- **Repeats:** 10 runs per configuration, averages reported
- **Platform (published numbers):** 4 vCPU / 16 GB cloud VM, **linux/amd64**
- **Python:** 3.13 (inside Docker Ubuntu 22.04 image from `benchmarks/Dockerfile`)
- **Go:** 1.26
- **Workers:** Caddy Snake uses one process worker in the benchmark Caddyfiles; Gunicorn uses 1 worker with 4 threads; Uvicorn uses 1 worker; the standalone ESGI line uses one `caddysnake.py` ESGI server process

## Reproduce

### Dedicated instance (Scaleway POP2-2C-8G)

For stable numbers on a clean dedicated instance, see [`benchmarks/scaleway_bench.sh`](https://github.com/mliezun/caddy-snake/blob/main/benchmarks/scaleway_bench.sh) (Scaleway CLI + SSH). Example:

```bash
BENCH_RSYNC_LOCAL=1 BENCH_SSH_IDENTITY="$HOME/.ssh/id_ed25519_scw" ./benchmarks/scaleway_bench.sh
```

### Any machine (Docker)

```bash
docker build -t caddy-snake-bench -f benchmarks/Dockerfile .
docker run --rm -v $(pwd)/benchmarks:/workspace/benchmarks caddy-snake-bench
```

Results go to `benchmarks/results.json` and `benchmarks/benchmark_chart.{png,svg}`. Copy the SVG into the docs site when publishing updated figures:

```bash
cp benchmarks/benchmark_chart.svg docs/static/img/benchmark_chart.svg
```

### What the benchmark does

1. Builds Caddy with the caddy-snake plugin from source
2. Creates a Python 3.13 virtual environment with Flask, FastAPI, Gunicorn, Uvicorn, uvloop, and **gevent** (for ESGI)
3. For each configuration: start servers, wait for `GET /hello`, warm up, then benchmark with hey (100 concurrent, 10 s) for 10 runs; record requests/sec, average latency, and P99
4. Writes `results.json` and generates the comparison chart
