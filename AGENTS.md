# AGENTS.md — Working on caddy-snake

This guide explains how to work on the caddy-snake project: environment setup, testing, debugging, profiling, and benchmarks.

---

## Pre-commit checklist

Before committing, always run:

1. **Go tests**: `go test -race -v .`
2. **Python tests**: `pytest caddysnake_test.py -v` (or `python -m pytest caddysnake_test.py -v`)
3. **Integration tests** — at minimum **Flask** and **FastAPI**:
   - `./tests/integration.sh flask 3.13`
   - `./tests/integration.sh fastapi 3.13`

See [Running tests](#running-tests) for details.

---

## Environment setup

### Go

- **Go 1.26** (see `go.mod`)
- **xcaddy**: `go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest`

For building Caddy with caddy-snake:

```bash
xcaddy build --with github.com/mliezun/caddy-snake=.
```

### Python

- **Python 3.10+** (3.13 recommended)
- Each integration test app has its own `tests/<app>/requirements.txt`

**Flask integration** (`tests/flask`):

```bash
cd tests/flask
python3.13 -m venv venv
source venv/bin/activate   # or venv\Scripts\activate on Windows
pip install -r requirements.txt
```

**FastAPI integration** (`tests/fastapi`):

```bash
cd tests/fastapi
python3.13 -m venv venv
source venv/bin/activate
pip install -r requirements.txt
```

**Python tests** (root-level `caddysnake_test.py`):

```bash
# From project root
pip install -r requirements-dev.txt   # pytest, pytest-cov, pytest-asyncio
# Run from project root so caddysnake and tests.test_apps are importable
```

### Integration tests (Docker)

For full CI-like integration tests without local Python/venv setup:

```bash
./tests/integration.sh <tool-name> <python-version>
# Examples:
./tests/integration.sh flask 3.13
./tests/integration.sh fastapi 3.13
```

Valid tools: `django`, `django_channels`, `flask`, `fastapi`, `simple_autoreload`, `simple_async`, `socketio`, `dynamic`  
Valid Python versions: `3.10`, `3.11`, `3.12`, `3.13`, `3.13-nogil`, `3.14`

Requires **Docker** (linux/amd64 container).

---

## Running tests

### Go tests

```bash
# Full suite with race detector (recommended before commit)
go test -race -v .

# Quick run without race detector
go test -v .

# With coverage
go test -race -coverprofile=coverage.out .
go tool cover -html=coverage.out
```

### Python tests

```bash
# From project root (so caddysnake and tests.test_apps are on PYTHONPATH)
pytest caddysnake_test.py -v

# With coverage
pytest caddysnake_test.py -v --cov=caddysnake --cov-report=term-missing

# With verbose output and stop on first failure
pytest caddysnake_test.py -vx
```

### Integration tests (Flask, FastAPI, etc.)

**Option A — Docker (recommended for CI parity):**

```bash
./tests/integration.sh flask 3.13
./tests/integration.sh fastapi 3.13
```

**Option B — Local (faster feedback):**

```bash
# 1. Set up venv and build Caddy (once per app)
cd tests/flask
python3.13 -m venv venv && source venv/bin/activate
pip install -r requirements.txt
CGO_ENABLED=0 xcaddy build --with github.com/mliezun/caddy-snake=../..

# 2. Start Caddy
./caddy run --config Caddyfile > caddy.log 2>&1 &

# 3. Wait for Caddy to be ready
timeout 60 bash -c 'while ! grep -q "finished cleaning storage units" caddy.log; do sleep 1; done'

# 4. Run integration test
source venv/bin/activate
python main_test.py

# 5. Stop Caddy
pkill -f "./caddy" || true
```

Same steps apply for `tests/fastapi`; the FastAPI test also expects `psutil` and performs extra checks on `caddy.log`.

---

## Debugging

### Go

- **Delve**: `dlv test .` or `dlv debug .` for interactive debugging
- **Verbose test output**: `go test -v .`
- **Race detector**: `go test -race .` to catch data races
- **Logging**: Caddy uses `go.uber.org/zap`; adjust log level in Caddyfile:
  ```json
  "log": { "level": "debug" }
  ```

### Python

- **pdb / breakpoint()**: Add `breakpoint()` in `caddysnake.py` or test files, then run:
  ```bash
  pytest caddysnake_test.py -v --pdb
  ```
- **pytest -v --pdb**: Drops into debugger on failure
- **Caddy logs**: Check `tests/<app>/caddy.log` for Python tracebacks during integration tests

### Caddy + Python integration

- Run Caddy with `--config Caddyfile` and watch `caddy.log` for Python errors
- Use `log { level debug }` in the Caddyfile for more detail

---

## Profiling

### Go

- **pprof (CPU)**:
  ```bash
  go test -cpuprofile=cpu.prof -race .
  go tool pprof cpu.prof
  ```
- **pprof (memory)**:
  ```bash
  go test -memprofile=mem.prof -race .
  go tool pprof mem.prof
  ```
- **pprof web UI**: `go tool pprof -http=:6060 cpu.prof`

### Python

- **cProfile**:
  ```bash
  python -m cProfile -o profile.stats -m pytest caddysnake_test.py -v
  python -c "import pstats; p = pstats.Stats('profile.stats'); p.sort_stats('cumulative'); p.print_stats(20)"
  ```
- **pytest with coverage**: `pytest caddysnake_test.py -v --cov=caddysnake --cov-report=html`

### Caddy (runtime)

Caddy exposes pprof endpoints when built with the standard config. You can add a debug route to capture profiles from a running Caddy instance; see Caddy’s documentation for pprof integration.

---

## Benchmarks

Benchmarks compare caddy-snake against traditional reverse-proxy setups (Flask + Gunicorn + Caddy, FastAPI + Uvicorn + Caddy).

### Run benchmarks (Docker)

```bash
# From repository root
docker build -t caddy-snake-bench -f benchmarks/Dockerfile .
docker run --rm -v $(pwd)/benchmarks:/workspace/benchmarks caddy-snake-bench
```

Results:

- `benchmarks/results.json`
- `benchmarks/benchmark_chart.png`
- `benchmarks/benchmark_chart.svg`

### Manual load testing with hey

Install [hey](https://github.com/rakyll/hey):

```bash
go install github.com/rakyll/hey@latest
```

Start Caddy with your app (e.g. Flask or FastAPI), then:

```bash
hey -c 100 -z 10s http://localhost:9080/hello
```
