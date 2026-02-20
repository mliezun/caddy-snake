# Benchmarks

Compares Caddy Snake performance against traditional reverse proxy setups.

## What's tested

| Configuration | Description |
|---|---|
| Flask + Gunicorn + Caddy | Gunicorn (1 worker, 4 threads) behind Caddy reverse proxy |
| Flask + Caddy Snake | Flask served directly by Caddy via caddy-snake (process worker) |
| FastAPI + Uvicorn + Caddy | Uvicorn (1 worker) behind Caddy reverse proxy |
| FastAPI + Caddy Snake | FastAPI served directly by Caddy via caddy-snake (process worker) |

## How to run

```bash
# From the repository root
docker build -t caddy-snake-bench -f benchmarks/Dockerfile .
docker run --rm -v $(pwd)/benchmarks:/workspace/benchmarks caddy-snake-bench
```

Results are saved to `benchmarks/results.json` and a chart is generated at `benchmarks/benchmark_chart.png`.

## Methodology

- Tool: [hey](https://github.com/rakyll/hey)
- 100 concurrent connections
- 10 second duration per test
- Warmup: 200 requests at 10 concurrency before each test
- Platform: Scaleway, Docker Ubuntu 22.04 on linux/amd64
- Python 3.13, Go 1.26
- Caddy Snake uses process workers; Gunicorn uses 1 worker with 4 threads; Uvicorn uses 1 worker

## Results

| Configuration | Requests/sec | Avg Latency (ms) | P99 Latency (ms) |
|---|---|---|---|
| Flask + Gunicorn + Caddy | 1,917 | 52.00 | 62.82 |
| Flask + Caddy Snake | 1,448 | 68.81 | 76.58 |
| FastAPI + Uvicorn + Caddy | 3,701 | 26.96 | 261.02 |
| FastAPI + Caddy Snake | 3,076 | 32.45 | 59.76 |
