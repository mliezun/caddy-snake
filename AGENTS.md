# AGENTS.md — Working on caddy-snake

This guide explains how to work on the caddy-snake project: environment setup, testing, debugging, profiling, and benchmarks.

---

## Pre-commit checklist

Before committing, run the full local QA suite (recommended):

```bash
./scripts/qa.sh
```

Or run checks individually:

1. **Pre-commit hooks**: `pre-commit run --all-files`
2. **Go tests**: `go test -race -v .`
3. **Go + caddytest** (in-process Caddy, on-demand TLS HTTPS): `go test -race -tags=caddytest -timeout 180s .`
4. **Python tests**: `pytest caddysnake_test.py -v`
5. **Static checks**: `golangci-lint run ./...`, `ruff check .`, `ty check` (or `uvx ty==0.0.55 check`)
6. **Integration tests** — at minimum **Flask** and **FastAPI**:
   - `./tests/integration.sh flask 3.13`
   - `./tests/integration.sh fastapi 3.13`
   - For **shared cache** changes: `./tests/integration.sh simple_cache 3.13`
7. **Embed-app** (optional, requires network): `cd cmd/embed-app && ./build.sh app.zip 3.13 && ./test_embed.sh embed-test`

Install hook once: `pre-commit install`

See [Running tests](#running-tests) and [Automated quality assurance](#automated-quality-assurance) for details.

---

## Documentation

When you implement or change a **user-facing feature** (Caddyfile directives, worker env vars, Python/Go APIs, cache protocol, CLI behavior, integration-test apps, etc.), **update the docs in the same PR** before merge:

1. **[`docs/docs/reference.md`](docs/docs/reference.md)** — authoritative configuration and API reference (Read the Docs).
2. **[`README.md`](README.md)** — overview and quick examples for GitHub visitors.
3. **[`AGENTS.md`](AGENTS.md)** — only when agent/workflow guidance changes (setup, QA, release steps).

For cache or multi-worker features, also cover: env vars, wire protocol (`CS*` commands if applicable), Python API, limits, security/trust model, and an integration test under `tests/` when behavior spans workers.

Build docs locally if you touch MkDocs content: `cd docs && mkdocs build` (also exercised in CI **Lint**).

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

- **Python 3.12+** (3.13 recommended)
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

Valid tools: `django`, `django_channels`, `flask`, `fastapi`, `simple_autoreload`, `simple_async`, `simple_esgi`, `simple_cache`, `socketio`, `dynamic`
Valid Python versions: `3.12`, `3.13`, `3.13-nogil`, `3.14`, `3.14-nogil`

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

# In-process Caddy integration (caddytest build tag — requires Python)
go test -race -tags=caddytest -timeout 180s .
```

### caddytest (in-process)

Use the **`caddytest`** build tag for **`caddysnake_caddytest_test.go`** (includes **HTTPS / on-demand TLS** coverage). Requires **`python`** on **`PATH`** and a generous timeout (**180s** is safe on CI-arm).

```bash
go test -race -tags=caddytest -timeout 180s .
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

### Run on Scaleway (POP2-2C-8G, linux/amd64)

For stable, CI-like numbers on **linux/amd64** without local Docker noise, provision a short-lived **POP2-2C-8G** instance, run the harness, fetch artifacts, and terminate:

```bash
./benchmarks/scaleway_bench.sh
# or: BENCH_RSYNC_LOCAL=1 ./benchmarks/scaleway_bench.sh   # rsync current tree
```

Requires [Scaleway CLI](https://github.com/scaleway/scaleway-cli) (`scw init`), `jq`, `tar`, and a **project SSH key** (see Scaleway console → SSH keys). See `benchmarks/scaleway_bench.sh` for env vars.

### After re-running benchmarks

Always update the following with the new results:

1. **README.md** — Benchmark table and footnote
2. **docs/docs/benchmarks.md** — Results table, methodology, and analysis
3. **benchmarks/README.md** — Results table
4. **docs/static/img/benchmark_chart.svg** — Copy from `benchmarks/benchmark_chart.svg`

### Manual load testing with hey

Install [hey](https://github.com/rakyll/hey):

```bash
go install github.com/rakyll/hey@latest
```

Start Caddy with your app (e.g. Flask or FastAPI), then:

```bash
hey -c 100 -z 10s http://localhost:9080/hello
```

---

## Automated quality assurance

CI runs lint, security, and test workflows on every PR and push to `main`.

### Local commands

| Command | Purpose |
|---------|---------|
| `./scripts/qa.sh` | Run pre-commit, Go/Python tests, linters, and security CLIs |
| `pre-commit run --all-files` | Hooks: Ruff, ty, Gitleaks, gofmt, shellcheck, actionlint |
| `golangci-lint run ./...` | Go static analysis |
| `ruff check .` / `ruff format --check .` | Python lint and format |
| `uvx ty==0.0.55 check` | Python type checking |
| `./scripts/audit-python-deps.sh` | pip-audit over all `requirements*.txt` files |

Install dev tools: `pip install -r requirements-dev.txt` (in a venv).

### CI workflows

| Workflow | Checks |
|----------|--------|
| **Lint** | pre-commit, golangci-lint, go vet, Ruff, ty, actionlint, shellcheck, docs build, cargo clippy |
| **Security** | govulncheck, pip-audit, npm audit, gosec, bandit, Semgrep, Gitleaks |
| **CodeQL** | Semantic SAST for Go and Python |
| **Go Tests** | race detector, coverage ≥ 65% |
| **Python Tests** | pytest coverage ≥ 50% |
| **Docker** | image build + Trivy scan (CRITICAL/HIGH) |
| **zizmor** | GitHub Actions workflow security |

### Post-merge (repository settings)

After merging the QA PR, configure on GitHub:

- [ ] **Branch protection** on `main`: require Lint, Go Tests, Python Tests, Security, CodeQL, zizmor
- [ ] **Secret scanning** and **push protection** (Settings → Code security)
- [ ] **Copilot Autofix** for code scanning alerts (Settings → Code security)
- [ ] **Renovate** app installed for dependency update PRs
- [ ] (Optional) `SNYK_TOKEN` secret for weekly Snyk Code scans
- [ ] (Optional) Semgrep AppSec account for AI-assisted triage

---

## Releases

Patch releases use semantic tags `v0.x.y` on `main`. Publishing a release triggers CI to build and attach Linux and macOS binaries (see `.github/workflows/build-binary.yml`, `build-standalone.yml`, `python-build.yml`, `docker-publish.yml`).

### Checklist

1. **Ensure `main` is green** — wait until all CI workflows on the latest `main` commit pass (`gh run list --branch main`, or watch in GitHub Actions).
2. **Choose the next patch tag** — inspect the latest release: `gh release list --limit 1` (e.g. after `v0.5.4`, tag `v0.5.5`).
3. **Bump the PyPI package version** in `cmd/cli/pyproject.toml` so it matches the tag you are about to create (without the `v` prefix). Commit and push to `main` before tagging:

```bash
# Example: preparing v0.5.5
# Set version = "0.5.5" in cmd/cli/pyproject.toml, then:
git add cmd/cli/pyproject.toml
git commit -m "Bump caddysnake PyPI version to 0.5.5."
git push origin main
```

The Python build workflow also sets the wheel version from the git tag in CI, but keeping `pyproject.toml` in sync avoids confusion and ensures local/`pip install` builds report the correct version.

4. **Create the GitHub release** (creates the tag on `main` and starts asset builds):

```bash
gh release create v0.5.5 --target main --title "v0.5.5" --notes "$(cat <<'EOF'
## What's Changed
* Short description by @author in https://github.com/mliezun/caddy-snake/pull/NNN

**Full Changelog**: https://github.com/mliezun/caddy-snake/compare/v0.5.4...v0.5.5
EOF
)"
```

5. **Wait for release workflows** — confirm `Caddy Binary`, `Caddy Standalone`, `Python Build Package`, and `Docker Publish` jobs succeed and assets appear on the release page (`gh release view v0.5.5`). Confirm the new version appears on [PyPI](https://pypi.org/project/caddysnake/).

To republish a tag to PyPI manually (e.g. after fixing the publish workflow), run **Python Build Package** via workflow dispatch on `main` with the tag name (e.g. `v0.5.7`).
