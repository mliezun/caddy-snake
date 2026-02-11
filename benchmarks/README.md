# Benchmarks

Compares Caddy Snake performance against traditional reverse proxy setups.

## What's tested

| Configuration | Description |
|---|---|
| Flask + Gunicorn + Caddy | Gunicorn (1 worker, 4 threads) behind Caddy reverse proxy |
| Flask + Caddy Snake | Flask served directly by Caddy via caddy-snake (thread worker) |
| FastAPI + Uvicorn + Caddy | Uvicorn (1 worker) behind Caddy reverse proxy |
| FastAPI + Caddy Snake | FastAPI served directly by Caddy via caddy-snake (thread worker) |

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
- Platform: Ubuntu 22.04 on linux/amd64
- Python 3.13, Go 1.26
- Caddy Snake uses thread workers; Gunicorn uses 1 worker with 4 threads; Uvicorn uses 1 worker
