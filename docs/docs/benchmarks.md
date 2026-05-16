---
sidebar_position: 7
---

# Benchmarks

Caddy Snake embeds Python directly inside Caddy, eliminating the overhead of a separate upstream process where the design allows. The harness compares **Flask (WSGI)**, **FastAPI (ASGI)**, and **ESGI (gevent)** on a minimal JSON `GET /hello` endpoint. The table below matches the committed [`benchmarks/results.json`](https://github.com/mliezun/caddy-snake/blob/main/benchmarks/results.json), produced on **Scaleway POP2-2C-8G (linux/amd64)** via [`benchmarks/scaleway_bench.sh`](https://github.com/mliezun/caddy-snake/blob/main/benchmarks/scaleway_bench.sh).

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
| Flask + Gunicorn + Caddy | 1,759 | 56.68 | 72.67 |
| **Flask + Caddy Snake** | **2,905** | **34.40** | **53.24** |
| FastAPI + Uvicorn + Caddy | 3,382 | 29.54 | 233.59 |
| **FastAPI + Caddy Snake** | **4,854** | **20.57** | **38.98** |
| ESGI (gevent) + Caddy reverse proxy | 5,011 | 19.92 | 50.45 |
| **ESGI + Caddy Snake** | **5,146** | **19.42** | **45.04** |

On this hardware, Flask and FastAPI see a clear gain from embedding; the ESGI pair is closer because both rows already use the same gevent gateway code path (reverse proxy vs embedded Go hop).

## Methodology

- **Tool:** [hey](https://github.com/rakyll/hey)
- **Concurrency:** 100 connections
- **Duration:** 10 seconds per test run
- **Warmup:** 200 requests at 10 concurrency before each configuration
- **Repeats:** 10 runs per configuration, averages reported
- **Platform (published numbers):** Scaleway **POP2-2C-8G**, **linux/amd64**
- **Python:** 3.13 (inside Docker Ubuntu 22.04 image from `benchmarks/Dockerfile`)
- **Go:** 1.26
- **Workers:** Caddy Snake uses one process worker in the benchmark Caddyfiles; Gunicorn uses 1 worker with 4 threads; Uvicorn uses 1 worker; the standalone ESGI line uses one `caddysnake.py` ESGI server process

## Reproduce

### Public numbers (Scaleway POP2-2C-8G)

See [`benchmarks/scaleway_bench.sh`](https://github.com/mliezun/caddy-snake/blob/main/benchmarks/scaleway_bench.sh) (Scaleway CLI + SSH). Example:

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
