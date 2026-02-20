---
sidebar_position: 1
---

# Quickstart

Let's discover **Caddy Snake in less than 5 minutes**.

Caddy Snake is a Caddy plugin that lets you run Python web apps directly inside Caddy — no reverse proxy needed. It embeds Python via the C API, so your WSGI or ASGI application runs in the same process as Caddy.

**Works with Flask, Django, FastAPI, and any other WSGI/ASGI framework.**

---

## Option 1: Install from PyPI

The fastest way to get started. Install with `pip` and you're ready to go:

```bash
pip install caddysnake
```

This installs the `caddysnake` command, which is a thin wrapper around a pre-compiled Caddy binary with the caddy-snake plugin and Python embedded. No system Python or C compiler required.

Available on [PyPI](https://pypi.org/project/caddysnake/) for Python 3.10 through 3.14 on Linux (x86_64 and ARM64).

### Usage

```bash
# Start a WSGI server
caddysnake --server-type wsgi --app main:app

# Start an ASGI server
caddysnake --server-type asgi --app main:app
```

This starts a server on port `9080` serving your app. See `caddysnake --help` for all available options:

| Flag | Description | Default |
|------|-------------|---------|
| `--server-type wsgi\|asgi` | **Required.** Type of Python app | — |
| `--app <module:var>` | **Required.** Python module and app variable (e.g. `main:app`) | — |
| `--domain <example.com>` | Enable HTTPS with automatic certificates | — |
| `--listen <addr>` | Custom listen address | `:9080` |
| `--workers <count>` | Number of worker processes | CPU count |
| `--static-path <path>` | Serve a static files directory | — |
| `--static-route <route>` | Route prefix for static files | `/static` |
| `--debug` | Enable debug logging | `false` |
| `--access-logs` | Enable access logs | `false` |

### Example

```bash
pip install caddysnake fastapi

caddysnake \
    --server-type asgi \
    --app main:app \
    --workers 4 \
    --static-path ./static \
    --access-logs
```

:::tip
The `caddysnake` PyPI package bundles a Caddy binary built with the caddy-snake plugin using [maturin](https://github.com/PyO3/maturin). Under the hood it runs `caddy python-server` with the same flags. See [how the CLI works](installation.md#pypi-package-caddysnake) for more details.
:::

---

## Option 2: Download a pre-built binary

Download a self-contained Caddy binary with Python embedded from the [latest release](https://github.com/mliezun/caddy-snake/releases). No system Python required — everything is bundled into a single executable.

```bash
# Download and extract
tar -xzf caddy-standalone-3.13-x86_64_v2-unknown-linux-gnu.tar.gz

# Start a server
./caddy python-server --server-type wsgi --app main:app
```

Pre-built binaries are available for Python 3.10 through 3.14 (including 3.13-nogil) on Linux x86_64 and ARM64. See the [Pre-built Binaries](installation.md#pre-built-standalone-binaries) page for details on how they work.

---

## Option 3: Build from source

```bash
CGO_ENABLED=1 xcaddy build --with github.com/mliezun/caddy-snake
```

### Requirements

- Python >= 3.10 + dev files
- C compiler and build tools
- Go >= 1.25 and [xcaddy](https://github.com/caddyserver/xcaddy)

Install on Ubuntu 24.04:

```bash
sudo apt-get install python3-dev build-essential pkg-config golang
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
```

### Example usage: FastAPI

`main.py`

```python
from fastapi import FastAPI

app = FastAPI()

@app.get("/hello-world")
def hello():
    return "Hello world!"
```

`Caddyfile`

```Caddyfile
http://localhost:9080 {
    route {
        python {
            module_asgi "main:app"
            lifespan on
        }
    }
}
```

Run:

```bash
pip install fastapi
./caddy run --config Caddyfile
```

```bash
curl http://localhost:9080/hello-world
# Hello world!
```

:::note
It's possible to enable/disable [lifespan events](https://fastapi.tiangolo.com/advanced/events/) by adding the `lifespan on|off` directive to your Caddy configuration. In the above case the lifespan events are enabled. Omitting the directive disables them by default.
:::

### Example usage: Flask

`main.py`

```python
from flask import Flask

app = Flask(__name__)

@app.route("/hello-world")
def hello():
    return "Hello world!"
```

`Caddyfile`

```Caddyfile
http://localhost:9080 {
    route {
        python {
            module_wsgi "main:app"
        }
    }
}
```

Run:

```bash
pip install Flask
./caddy run --config Caddyfile
```

```bash
curl http://localhost:9080/hello-world
# Hello world!
```

---

## Option 4: Use a Docker image

Docker images are available with Python 3.10, 3.11, 3.12, 3.13, and 3.14:

```Dockerfile
FROM mliezun/caddy-snake:latest-py3.13

WORKDIR /app
COPY . /app

CMD ["caddy", "run", "--config", "/app/Caddyfile"]
```

Images are published to both registries:

- [Docker Hub](https://hub.docker.com/r/mliezun/caddy-snake)
- [GitHub Container Registry](https://github.com/mliezun/caddy-snake/pkgs/container/caddy-snake)

### Build with Docker

There's a template file in the project: [builder.Dockerfile](https://github.com/mliezun/caddy-snake/blob/main/builder.Dockerfile). It supports build arguments to configure which Python or Go version to use.

```bash
# Build the Docker image
docker build -f builder.Dockerfile --build-arg PY_VERSION=3.13 -t caddy-snake-builder .

# Extract the caddy binary to your current directory
docker run --rm -v $(pwd):/output caddy-snake-builder
```

Make sure to match the Python version with your target environment.

---

## Virtual environments

You can point Caddy Snake to a Python virtual environment using the `venv` directive:

```Caddyfile
python {
    module_wsgi "main:app"
    venv "./venv"
}
```

Behind the scenes, this appends `venv/lib/python3.x/site-packages` to `sys.path` so installed packages are available to your app.

:::note
The venv packages are added to the global `sys.path`, which means all Python apps served by Caddy share the same packages.
:::

---

## Platform support

| Platform       | Notes                    |
|----------------|--------------------------|
| Linux (x86_64) | Primary platform         |
| Linux (arm64)  | Full support             |
| macOS          | Full support             |

**Python versions:** 3.10, 3.11, 3.12, 3.13, 3.13-nogil (free-threaded), 3.14

---

## What's next?

- Learn about [Installation & Distribution](installation.md) — PyPI, pre-built binaries, and how they work
- Learn about all [Configuration Options](reference.md)
- See more [Examples](examples.md) with Flask, Django, FastAPI, Socket.IO, and more
- Understand the [Architecture](architecture.md) and how it all works
- Check out the [Benchmarks](benchmarks.md) comparing Caddy Snake to traditional reverse proxy setups
