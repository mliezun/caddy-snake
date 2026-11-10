# caddysnake CLI

[![PyPI](https://img.shields.io/pypi/v/caddysnake)](https://pypi.org/project/caddysnake/)

The `caddysnake` package is available on [PyPI](https://pypi.org/project/caddysnake/) and provides a CLI to serve Python WSGI/ASGI applications powered by Caddy.

## Install

```bash
pip install caddysnake
```

Available for Python 3.13 through 3.15 on Linux (x86_64, ARM64 and RISC-V 64), macOS (x86_64 and ARM64) and Windows (x86_64 and ARM64).

## Usage

```bash
# Start a WSGI server
caddysnake --server-type wsgi --app main:app

# Start an ASGI server
caddysnake --server-type asgi --app main:app
```

## Apps platform

`caddysnake apps` runs a small platform where people build and share internal web apps through their own AI assistant: each app is served at `/<app-name>/` with its own Postgres database, versions, drafts and roll back.

```bash
caddysnake apps                         # http://localhost:9080, assistants connect to /_apps/mcp
caddysnake apps user add ana --admin    # with --require-login
caddysnake apps call list               # run a tool from the command line
caddysnake apps install                 # run it as a service that starts by itself
```

It needs the Postgres programs installed (it starts its own private database). See the [apps documentation](https://caddy-snake.readthedocs.io/en/latest/docs/apps).

## CLI Options

Python-handler flags mirror the Caddyfile `python` block. Extra flags cover listen address, HTTPS, and static files.

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--server-type` | `-t` | **Required.** Type of Python app: `wsgi`, `asgi`, or `auto` (detect WSGI vs ASGI) | — |
| `--app` | `-a` | **Required.** Python module and app variable (e.g. `main:app`) | — |
| `--domain` | `-d` | Domain name for HTTPS with automatic certificates | — |
| `--listen` | `-l` | Custom listen address | `:9080` |
| `--workers` | `-w` | Number of worker processes (0 = CPU count) | `0` |
| `--max-dynamic-apps` | | Max distinct dynamic Python apps | `128` |
| `--request-body-max-size` | | Maximum HTTP request body size (e.g. `1KiB`, `2GB`) | unlimited |
| `--mount-prefix` | | URL path prefix the app is mounted under (e.g. `/tools/inventory`) | — |
| `--memory-limit` | | Memory ceiling per dynamically loaded app (e.g. `256MiB`); an app over it is restarted | — |
| `--python-path` | | Path to the Python interpreter | — |
| `--working-dir` | | Working directory for the Python app | — |
| `--venv` | | Path to a Python virtual environment | — |
| `--env-file` | | Dotenv file for worker env (repeatable) | — |
| `--env-var` | | Inline worker env var `NAME=VALUE` (repeatable) | — |
| `--start-timeout` | | Worker readiness wait (`120s` default; use `=-1` or `forever` for indefinite) | `120s` |
| `--runtime` | | WSGI: `sync\|gevent`; ASGI: `native\|uvloop` | per interface |
| `--lifespan` | | ASGI lifespan events (`on\|off`) | `off` |
| `--cache-mode` | | Cache topology: `local\|cluster` | `local` |
| `--cache-listen` | | Cluster peer TCP listener (`host:port`) | — |
| `--cache-advertise` | | Dialable address identifying this peer | — |
| `--cache-peer` | | Static cluster peer address (repeatable) | — |
| `--cache-namespace` | | Namespace shared by every cluster peer | — |
| `--cache-secret` | | Shared peer secret (16–4096 bytes) | — |
| `--static-path` | | Path to a static files directory | — |
| `--static-route` | | Route prefix for static files | `/static` |
| `--debug` | | Enable debug logging | `false` |
| `--access-logs` | | Enable access logs | `false` |
| `--autoreload` | | Watch `.py` files and reload on changes | `false` |

## How it works

This package is built with [maturin](https://github.com/PyO3/maturin) and distributed as platform-specific wheel files. Each wheel bundles a pre-compiled Caddy binary with the caddy-snake plugin (`caddysnake-cli`). The Python CLI wrapper (`caddysnake_cli.py`) uses [click](https://click.palletsprojects.com/) to parse arguments and then executes the bundled Caddy binary with `os.execv`.

The package is built and published automatically on tagged releases by the [`python-build.yml`](../../.github/workflows/python-build.yml) GitHub Actions workflow.

## Full Documentation

See https://caddy-snake.readthedocs.io for complete documentation, including:

- [Quickstart](https://caddy-snake.readthedocs.io/en/latest/docs/intro) — get started in 5 minutes
- [Installation](https://caddy-snake.readthedocs.io/en/latest/docs/installation) — PyPI, standalone binaries, Docker, and more
- [Configuration Reference](https://caddy-snake.readthedocs.io/en/latest/docs/reference) — all Caddyfile directives
- [Examples](https://caddy-snake.readthedocs.io/en/latest/docs/examples) — Flask, Django, FastAPI, Socket.IO, dynamic modules, autoreload
- [Architecture](https://caddy-snake.readthedocs.io/en/latest/docs/architecture) — how it works under the hood
