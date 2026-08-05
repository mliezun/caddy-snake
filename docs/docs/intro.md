---
sidebar_position: 1
title: Quickstart
description: Run a Python web app with Caddy Snake in under five minutes
---

# Quickstart

Caddy Snake runs Python web apps **inside Caddy** — no separate Gunicorn or Uvicorn process. It works with Flask, Django, FastAPI, and any other WSGI/ASGI (or [ESGI](esgi.md)) app.

## Install and run

```bash
pip install caddysnake fastapi
```

`main.py`:

```python
from fastapi import FastAPI

app = FastAPI()

@app.get("/hello")
def hello():
    return {"message": "Hello world!"}
```

```bash
caddysnake --server-type asgi --app main:app --lifespan on
curl http://127.0.0.1:9080/hello
```

That starts Caddy with the plugin on port `9080`. Use `--domain example.com` for automatic HTTPS.

:::tip Flask instead?
```bash
pip install caddysnake flask
caddysnake --server-type wsgi --app main:app
```
:::

## Prefer a Caddyfile?

```caddyfile
http://localhost:9080 {
    python /* {
        module_asgi "main:app"
        lifespan on
    }
}
```

```bash
caddy run --config Caddyfile
```

## Other install options

| Method | When to use |
|--------|-------------|
| **[PyPI](installation.md#pypi-package-caddysnake)** (`pip install caddysnake`) | Day-to-day development |
| **[Standalone binary](installation.md#pre-built-standalone-binaries)** | No system Python on the host |
| **[Docker](installation.md#docker-images)** | Containers |
| **[Build from source](installation.md#building-from-source)** | Custom Caddy builds |

Full CLI flags live in the [configuration reference](reference.md#python-server-command).

## Next steps

- [Examples](examples.md) — Django, Socket.IO, multi-tenant hostnames, autoreload
- [Configuration reference](reference.md) — every `python` directive
- [Architecture](architecture.md) — workers, sockets, dynamic apps
- [Blog: branch previews](../blog/branch-previews) — one-box PR previews with DB clones
