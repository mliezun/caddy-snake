---
title: Installation
description: Install Caddy Snake via PyPI, standalone binary, Docker, or source
sidebar_position: 2
---

# Installation

Pick one path. For a five-minute hello world, use [Quickstart](intro.md).

## Which method?

| Method | System Python | Best for |
|--------|---------------|----------|
| **PyPI** | Required | Development, day-to-day use |
| **Standalone binary** | Embedded | Production hosts without Python |
| **App binary** | Embedded | Ship one executable (see [embed-app](embed-app.md)) |
| **Docker** | In the image | Containers |
| **Source** | Required at runtime | Custom Caddy builds |

---

## PyPI package {#pypi-package-caddysnake}

```bash
pip install caddysnake
caddysnake --server-type asgi --app main:app
```

Requires Python ≥ 3.13 on Linux (x86_64 / ARM64 / RISC-V 64) or macOS (x86_64 / ARM64). The wheel ships a prebuilt Caddy binary with the plugin; workers use your environment’s Python.

`caddysnake` is a thin wrapper around `caddy python-server`. Full flags: [configuration reference](reference.md#python-server-command).

---

## Pre-built standalone binaries {#pre-built-standalone-binaries}

Self-contained: Caddy + plugin + a [python-build-standalone](https://github.com/astral-sh/python-build-standalone) interpreter. No system Python.

Download from [GitHub Releases](https://github.com/mliezun/caddy-snake/releases):

```
caddy-standalone-{python}-{arch}.tar.gz
```

Examples: `3.13` / `3.14` / `3.15`, `nogil` variants, Linux (x86_64, ARM64, RISC-V 64) and macOS (x86_64, ARM64). The RISC-V file uses `riscv64-unknown-linux-gnu`.

```bash
tar -xzf caddy-standalone-3.13-x86_64_v2-unknown-linux-gnu.tar.gz
./caddy python-server --server-type wsgi --app main:app
```

Install app deps into a venv created with the embedded interpreter, then point at it:

```bash
./caddy python -m venv myenv
source myenv/bin/activate && pip install fastapi
```

```caddyfile
python {
    module_asgi "main:app"
    venv "./myenv"
}
```

---

## Docker images {#docker-images}

```bash
docker pull mliezun/caddy-snake:latest-py3.13
# or: ghcr.io/mliezun/caddy-snake:latest-py3.13
```

Multi-arch images (`linux/amd64`, `linux/arm64`, `linux/riscv64`). Tags for Python `3.13`, `3.14`, and `3.15` on [Docker Hub](https://hub.docker.com/r/mliezun/caddy-snake) and [GHCR](https://github.com/mliezun/caddy-snake/pkgs/container/caddy-snake).

```Dockerfile
FROM mliezun/caddy-snake:latest-py3.13
WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt
COPY . .
CMD ["caddy", "run", "--config", "/app/Caddyfile"]
```

---

## Building from source {#building-from-source}

```bash
go install github.com/caddyserver/xcaddy/cmd/xcaddy@v0.4.6
CGO_ENABLED=0 xcaddy build --with github.com/mliezun/caddy-snake
```

Needs Go ≥ 1.26 and Python ≥ 3.13 on the target host at runtime.

Or build in Docker and extract the binary:

```bash
docker build -f builder.Dockerfile --build-arg PY_VERSION=3.13 -t caddy-snake-builder .
docker run --rm -v "$(pwd)":/output caddy-snake-builder
```

Match `PY_VERSION` to the Python on the machine that will run the binary.
