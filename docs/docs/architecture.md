---
title: Architecture
description: How Caddy Snake routes requests to Python workers
sidebar_position: 5
---

# Architecture

Caddy Snake is a Caddy plugin that forwards HTTP to **Python worker subprocesses**. Workers speak WSGI, ASGI, or ESGI to your app. There is no CGO and no embedded Python C API in the Caddy process.

---

## Request flow

<img src="/en/latest/img/caddysnake-diagram.png" alt="Request flow from Caddy to a Python worker" width="600" />

1. Caddy matches a `python` handler.
2. The plugin picks a worker (round-robin).
3. The request is proxied over a Unix domain socket (loopback TCP on Windows).
4. [`caddysnake.py`](https://github.com/mliezun/caddy-snake/blob/main/caddysnake.py) translates to WSGI/ASGI/ESGI and writes the response back.

Workers are spawned with the bundled `caddysnake.py` script. The interpreter comes from `python_path`, a configured `venv`, `PATH`, or (for standalone builds) an embedded distribution.

---

## Workers

| | Process workers (default) | `isolation docker` |
|---|---|---|
| Unit | OS process | Container |
| Parallelism | One GIL per worker | Same |
| Crash blast radius | One worker | One container |
| Filesystem / env | Shared with Caddy UID | Bind-mounts + explicit env only |

`workers N` starts N units. See [Isolation](isolation.md).

---

## Autoreload

With `autoreload`, a filesystem watcher (fsnotify) watches the working directory for `.py` changes (500ms debounce), starts a new worker group, and swaps it in under a read/write lock. Failed reloads serve HTTP 503 until the next success.

:::warning Production tip
Autoreload waits on in-flight requests. Long-lived WebSockets can stall a reload. Prefer `caddy reload` for sticky production sessions; keep autoreload for development and disposable preview apps. See the [branch previews](../blog/branch-previews) case study.
:::

---

## Dynamic apps

Placeholders in `module_*`, `working_dir`, `venv`, `env_file`, and `env_var` are resolved per request. Apps are created lazily and cached (default **128** apps, ~**30m** idle TTL). Over capacity → HTTP 503.

Each dynamic working directory can have its own autoreload watcher; a change only evicts apps for that directory.

Details and security notes: [Configuration reference](reference.md#dynamic-module-loading).

---

## Protocols

- **WSGI** — PEP 3333 env + `start_response`
- **ASGI** — HTTP and WebSocket; optional lifespan
- **ESGI** — gevent-only sync gateway; see [ESGI](esgi.md)

---

## Limits to know

- The [shared worker cache](reference.md#shared-worker-cache) is **not** a tenant boundary — prefix keys or use an external store.
- Dynamic app cache is bounded; large tenant counts need higher limits or external routing.
- Docker isolation hardens the worker sandbox; it does not isolate the shared cache.
