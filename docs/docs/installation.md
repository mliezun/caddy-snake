---
title: Installation & Distribution
description: All the ways to install Caddy Snake — PyPI, pre-built binaries, Docker, and building from source
sidebar_position: 5
---

# Installation & Distribution

Caddy Snake is available in several forms, each suited to different use cases. This page covers how each distribution method works, where to get it, and when to use which.

---

## PyPI Package (`caddysnake`)

The [`caddysnake`](https://pypi.org/project/caddysnake/) package on PyPI is the easiest way to install and run Caddy Snake. It provides a `caddysnake` CLI command that serves your Python web app with a single command.

### Install

```bash
pip install caddysnake
```

**Requirements:** Python >= 3.10, Linux (x86_64 or ARM64)

Available Python versions: 3.10, 3.11, 3.12, 3.13, 3.14

### Usage

```bash
# Serve a WSGI app (Flask, Django, etc.)
caddysnake --server-type wsgi --app main:app

# Serve an ASGI app (FastAPI, Starlette, etc.)
caddysnake --server-type asgi --app main:app

# With HTTPS, workers, and static files
caddysnake \
    --server-type asgi \
    --app main:app \
    --domain example.com \
    --workers 4 \
    --static-path ./static \
    --access-logs
```

### CLI Options

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

### How it works

The PyPI package is built with [maturin](https://github.com/PyO3/maturin) and distributed as platform-specific wheel files. Each wheel contains:

1. **`caddysnake-cli`** — a pre-compiled Caddy binary built with the caddy-snake plugin via [xcaddy](https://github.com/caddyserver/xcaddy)
2. **`caddysnake_cli.py`** — a Python CLI wrapper (using [click](https://click.palletsprojects.com/)) that translates command-line flags into `caddy python-server` arguments and executes the bundled Caddy binary

When you run `caddysnake --server-type wsgi --app main:app`, the wrapper:
1. Locates the bundled `caddysnake-cli` binary in the package directory
2. Builds the equivalent `caddy python-server` command with all the flags
3. Uses `os.execv` to replace the current process with the Caddy binary

The package uses the system Python at runtime — the `caddysnake-cli` binary is linked against your installed Python version. This is different from the [pre-built standalone binaries](#pre-built-standalone-binaries), which embed a full Python distribution.

### Build pipeline

The package is built and published automatically on tagged releases (`v*.*.*`) by the [`python-build.yml`](https://github.com/mliezun/caddy-snake/blob/main/.github/workflows/python-build.yml) GitHub Actions workflow:

1. Caddy is built with the caddy-snake plugin using `xcaddy` inside a Docker container (`builder.Dockerfile`)
2. The resulting `caddy` binary is moved into the `cmd/cli/` directory as `caddysnake-cli`
3. `maturin build --release` packages it into a Python wheel
4. The wheel is published to PyPI with `twine`

This runs for each combination of Python version (3.10–3.14) and architecture (x86_64, ARM64), producing platform-specific wheels.

---

## Pre-built Standalone Binaries

The standalone binaries are fully self-contained executables that bundle both Caddy (with the caddy-snake plugin) **and** a complete Python distribution. No system Python, no pip, no dependencies — just download and run.

### Download

Pre-built binaries are available from the [GitHub Releases](https://github.com/mliezun/caddy-snake/releases) page. The naming convention is:

```
caddy-standalone-{python-version}-{architecture}.tar.gz
```

For example:
- `caddy-standalone-3.13-x86_64_v2-unknown-linux-gnu.tar.gz` — Python 3.13, Linux x86_64
- `caddy-standalone-3.13-aarch64-unknown-linux-gnu.tar.gz` — Python 3.13, Linux ARM64
- `caddy-standalone-3.13-nogil-x86_64_v2-unknown-linux-gnu.tar.gz` — Python 3.13 free-threaded

### Available versions

| Python | Architectures | Notes |
|--------|--------------|-------|
| 3.10 | x86_64, ARM64 | |
| 3.11 | x86_64, ARM64 | |
| 3.12 | x86_64, ARM64 | |
| 3.13 | x86_64, ARM64 | |
| 3.13-nogil | x86_64, ARM64 | Free-threaded (PEP 703) |
| 3.14 | x86_64, ARM64 | |

### Usage

```bash
# Download and extract
tar -xzf caddy-standalone-3.13-x86_64_v2-unknown-linux-gnu.tar.gz

# Use the CLI shorthand
./caddy python-server --server-type wsgi --app main:app

# Or use a Caddyfile for full configuration
./caddy run --config Caddyfile
```

Since the standalone binary comes with its own Python, you don't need to install Python on the host. However, your app's dependencies (e.g. Flask, FastAPI) need to be available. You can install them into a virtual environment and point to it with the `venv` directive:

```bash
# Create a venv using the embedded Python
./caddy python -m venv myenv
source myenv/bin/activate
pip install fastapi

# Run with the venv
./caddy python-server --server-type asgi --app main:app
```

Or in a Caddyfile:

```Caddyfile
python {
    module_asgi "main:app"
    venv "./myenv"
}
```

### How it works

The standalone binary is a Go program (`cmd/embed/main.go`) that uses Go's `//go:embed` directive to bundle two files:

1. **`caddy`** — the Caddy binary built with the caddy-snake plugin
2. **`python-standalone.tar.gz`** — a [python-build-standalone](https://github.com/astral-sh/python-build-standalone) distribution (maintained by [Astral](https://astral.sh/))

When you run the standalone binary:

1. It creates a temporary directory and extracts the embedded Python distribution into it
2. It writes the embedded Caddy binary to another temporary directory
3. It sets up the environment:
   - `PYTHONHOME` — points to the extracted Python distribution
   - `LD_LIBRARY_PATH` — includes the Python shared libraries
4. It executes the Caddy binary with the provided arguments, passing through stdin/stdout/stderr
5. On exit, both temporary directories are cleaned up automatically

This means the binary is large (typically ~16 MB) because it contains a full Python interpreter, but it requires zero dependencies on the host system.

### The `pybs.py` helper

The build process uses a helper script (`cmd/embed/pybs.py`) to download Python distributions from the [python-build-standalone](https://github.com/astral-sh/python-build-standalone) project. This script:

- Fetches release information from the GitHub API
- Selects the appropriate asset based on Python version, architecture, build config, and content type
- Supports all python-build-standalone architectures (Linux, macOS, Windows) and build configurations (pgo+lto, freethreaded, debug, etc.)
- Handles GitHub API rate limiting with exponential backoff
- Uses Levenshtein distance to suggest closest matches when an exact match isn't found

You can use `pybs.py` directly to download Python distributions:

```bash
# Download Python 3.13 for Linux x86_64 (default)
python3 cmd/embed/pybs.py latest

# Download Python 3.14 for macOS ARM
python3 cmd/embed/pybs.py latest \
    --python-version 3.14 \
    --architecture aarch64-apple-darwin

# List all available options
python3 cmd/embed/pybs.py latest --list-options
```

### Build pipeline

The standalone binaries are built by the [`build-standalone.yml`](https://github.com/mliezun/caddy-snake/blob/main/.github/workflows/build-standalone.yml) GitHub Actions workflow:

1. Caddy is built with the caddy-snake plugin using `xcaddy` in the `cmd/embed/` directory
2. `pybs.py` downloads the appropriate python-build-standalone distribution
3. The Python distribution is compressed as `python-standalone.tar.gz`
4. `go build main.go` compiles the Go wrapper, which embeds both the Caddy binary and the Python distribution
5. The resulting self-contained binary is uploaded as a release artifact

For the `3.13-nogil` variant, a freethreaded python-build-standalone distribution is used instead.

---

## Docker Images

Docker images come with Caddy Snake pre-installed and a specific Python version available.

### Available images

```bash
# Docker Hub
docker pull mliezun/caddy-snake:latest-py3.13

# GitHub Container Registry
docker pull ghcr.io/mliezun/caddy-snake:latest-py3.13
```

Available Python versions: `3.10`, `3.11`, `3.12`, `3.13`, `3.14`

### Usage

```Dockerfile
FROM mliezun/caddy-snake:latest-py3.13

WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt

COPY . .
CMD ["caddy", "run", "--config", "/app/Caddyfile"]
```

### Registries

- [Docker Hub](https://hub.docker.com/r/mliezun/caddy-snake)
- [GitHub Container Registry](https://github.com/mliezun/caddy-snake/pkgs/container/caddy-snake)

---

## Building from Source

For maximum control, you can build Caddy with the caddy-snake plugin from source.

### Requirements

- Python >= 3.10 + dev files (`python3-dev`)
- C compiler and build tools (`build-essential`, `pkg-config`)
- Go >= 1.25
- [xcaddy](https://github.com/caddyserver/xcaddy)

### Build

```bash
# Install dependencies (Ubuntu 24.04)
sudo apt-get install python3-dev build-essential pkg-config golang
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest

# Build Caddy with caddy-snake
CGO_ENABLED=1 xcaddy build --with github.com/mliezun/caddy-snake
```

The resulting `caddy` binary uses your system Python and requires it at runtime.

### Build with Docker

If you don't want to install Go and the build tools on your system, you can use Docker:

```bash
# Build the Docker image (uses builder.Dockerfile)
docker build -f builder.Dockerfile --build-arg PY_VERSION=3.13 -t caddy-snake-builder .

# Extract the caddy binary
docker run --rm -v $(pwd):/output caddy-snake-builder
```

Make sure the `PY_VERSION` matches the Python version on your target system.

---

## Comparison

| Method | System Python required | Internet access needed | Caddyfile support | CLI shorthand | Best for |
|--------|----------------------|----------------------|-------------------|---------------|----------|
| **PyPI** (`pip install caddysnake`) | Yes | At install time | No (CLI only) | `caddysnake` | Quick start, development |
| **Standalone binary** | No (embedded) | No (self-contained) | Yes | `./caddy python-server` | Production, air-gapped environments |
| **Docker** | No (in container) | At build time | Yes | `caddy python-server` | Containerized deployments |
| **Build from source** | Yes | At build time | Yes | `./caddy python-server` | Custom builds, macOS, Windows |

:::tip
- Use **PyPI** for the fastest way to get started or for development
- Use **standalone binaries** for production Linux deployments where you want zero dependencies
- Use **Docker** for containerized environments
- **Build from source** when you need macOS/Windows support or want to pin specific versions
:::
