# caddysnake CLI

[![PyPI](https://img.shields.io/pypi/v/caddysnake)](https://pypi.org/project/caddysnake/)

The `caddysnake` package is available on [PyPI](https://pypi.org/project/caddysnake/) and provides a CLI to serve Python WSGI/ASGI applications powered by Caddy.

## Install

```bash
pip install caddysnake
```

Available for Python 3.10 through 3.14 on Linux (x86_64 and ARM64).

## Usage

```bash
# Start a WSGI server
caddysnake --server-type wsgi --app main:app

# Start an ASGI server
caddysnake --server-type asgi --app main:app
```

## CLI Options

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--server-type` | `-t` | **Required.** Type of Python app: `wsgi` or `asgi` | — |
| `--app` | `-a` | **Required.** Python module and app variable (e.g. `main:app`) | — |
| `--domain` | `-d` | Domain name for HTTPS with automatic certificates | — |
| `--listen` | `-l` | Custom listen address | `:9080` |
| `--workers` | `-w` | Number of worker processes (0 = CPU count) | `0` |
| `--static-path` | | Path to a static files directory | — |
| `--static-route` | | Route prefix for static files | `/static` |
| `--debug` | | Enable debug logging | `false` |
| `--access-logs` | | Enable access logs | `false` |

## How it works

This package is built with [maturin](https://github.com/PyO3/maturin) and distributed as platform-specific wheel files. Each wheel bundles a pre-compiled Caddy binary with the caddy-snake plugin (`caddysnake-cli`). The Python CLI wrapper (`caddysnake_cli.py`) uses [click](https://click.palletsprojects.com/) to parse arguments and then executes the bundled Caddy binary with `os.execv`.

The package is built and published automatically on tagged releases by the [`python-build.yml`](../../.github/workflows/python-build.yml) GitHub Actions workflow.

## Full Documentation

See https://caddy-snake.readthedocs.io for complete documentation, including:

- [Quickstart](https://caddy-snake.readthedocs.io/en/latest/docs/intro) — get started in 5 minutes
- [Installation & Distribution](https://caddy-snake.readthedocs.io/en/latest/docs/installation) — PyPI, standalone binaries, Docker, and more
- [Configuration Reference](https://caddy-snake.readthedocs.io/en/latest/docs/reference) — all Caddyfile directives
- [Examples](https://caddy-snake.readthedocs.io/en/latest/docs/examples) — Flask, Django, FastAPI, Socket.IO, dynamic modules, autoreload
- [Architecture](https://caddy-snake.readthedocs.io/en/latest/docs/architecture) — how it works under the hood
