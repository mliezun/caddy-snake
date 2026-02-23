---
sidebar_position: 5
---

# Benchmarks

Caddy Snake embeds Python directly inside Caddy, eliminating the overhead of a separate reverse proxy. This page compares Caddy Snake against traditional deployment setups — Caddy Snake is **2.4x faster** than Flask+Gunicorn and **1.6x faster** than FastAPI+Uvicorn.

## Test configurations

| Configuration | Description |
|---|---|
| Flask + Gunicorn + Caddy | Gunicorn (1 worker, 4 threads) behind Caddy reverse proxy |
| Flask + Caddy Snake | Flask served directly by Caddy via caddy-snake (process worker) |
| FastAPI + Uvicorn + Caddy | Uvicorn (1 worker) behind Caddy reverse proxy |
| FastAPI + Caddy Snake | FastAPI served directly by Caddy via caddy-snake (process worker) |

All configurations serve a minimal JSON "Hello, World!" endpoint.

## Results

![Benchmark Chart](../static/img/benchmark_chart.svg)

| Configuration | Requests/sec | Avg Latency (ms) | P99 Latency (ms) |
|---|---|---|---|
| Flask + Gunicorn + Caddy | 1,592 | 63.81 | 89.18 |
| **Flask + Caddy Snake** | **3,782** | **26.42** | **41.46** |
| FastAPI + Uvicorn + Caddy | 3,537 | 28.20 | 282.19 |
| **FastAPI + Caddy Snake** | **5,730** | **17.44** | **31.11** |

Caddy Snake significantly outperforms traditional reverse proxy setups. For Flask (WSGI), Caddy Snake delivers **2.4x** the throughput of Gunicorn with less than half the latency. For FastAPI (ASGI), Caddy Snake achieves **1.6x** the throughput of Uvicorn with much lower P99 latency (31ms vs 282ms), meaning faster and more consistent response times.

## Methodology

- **Tool:** [hey](https://github.com/rakyll/hey)
- **Concurrency:** 100 connections
- **Duration:** 10 seconds per test
- **Warmup:** 200 requests at 10 concurrency before each test
- **Platform:** Scaleway, Docker Ubuntu 22.04 on linux/amd64
- **Python:** 3.13
- **Go:** 1.26
- **Workers:** Caddy Snake uses process workers; Gunicorn uses 1 worker with 4 threads; Uvicorn uses 1 worker

## Reproduce

Run the benchmarks yourself from the repository root:

```bash
docker build -t caddy-snake-bench -f benchmarks/Dockerfile .
docker run --rm -v $(pwd)/benchmarks:/workspace/benchmarks caddy-snake-bench
```

Results are saved to `benchmarks/results.json` and charts are generated at `benchmarks/benchmark_chart.png` and `benchmarks/benchmark_chart.svg`.

### What the benchmark does

1. Builds Caddy with the caddy-snake plugin from source
2. Sets up a Python 3.13 virtual environment with Flask, FastAPI, Gunicorn, and Uvicorn
3. For each configuration:
   - Starts the server(s)
   - Runs a warmup phase (200 requests)
   - Benchmarks with 100 concurrent connections for 10 seconds
   - Records requests/sec, average latency, and P99 latency
4. Generates a comparison chart and results JSON
