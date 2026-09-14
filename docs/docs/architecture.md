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
Autoreload waits on in-flight requests. Long-lived WebSockets can stall a reload. Prefer `caddy reload` for sticky production sessions; keep autoreload for development and disposable preview apps.
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

### Request path encoding

The HTTP/1.1 request-target is treated as raw octets (latin-1 on the Python side). Percent-decoding (`%HH`) is applied once, then each protocol presents those octets differently:

| Interface | Path field | Presentation |
|-----------|------------|----------------|
| **WSGI** | `PATH_INFO` | Percent-decoded octets as a **latin-1** `str` (PEP 3333). Frameworks such as Flask/Werkzeug then `encode("latin-1").decode("utf-8")` to recover Unicode. |
| **ASGI** | `scope["path"]` | Percent-decoded octets as **UTF-8** text (invalid sequences → U+FFFD). `scope["raw_path"]` is the path bytes **as received by the Python worker**, without percent-decoding. The Go reverse-proxy hop may percent-encode a client’s raw UTF-8 request-target, so `raw_path` can be `%C3%A5…` even when the browser sent raw UTF-8; decoded `path` / WSGI `PATH_INFO` still round-trip correctly. |
| **ESGI** | `scope["path"]` | Same UTF-8 text rule as ASGI (ESGI 0.1-draft). `query_string` is **not** percent-decoded. |

`QUERY_STRING` / ASGI `query_string` stay encoded. This matches Gunicorn/Uvicorn so `/åäö` and `/%C3%A5%C3%A4%C3%B6` round-trip the same way.

Tricky encodings we explicitly cover:

- **UTF-8 vs ISO-8859-1 percent escapes** — `%C3%A5` is UTF-8 å; `%E5` is a single latin-1 octet (WSGI `PATH_INFO` keeps U+00E5; ASGI/ESGI replace it with U+FFFD).
- **Hex case** — `%c3%a5` and `%C3%a5` decode the same.
- **NFC vs NFD** — `caf%C3%A9` and `cafe%CC%81` stay distinct (no Unicode normalization).
- **Invalid UTF-8** — truncated sequences, overlong encodings, C1 controls (`%80`–`%9F`), and IIS-style `%uXXXX`. When the request reaches Python, `%u00E5` is left as the six literal characters `/` `%` `u` `0` `0` `E` `5`. Go's `net/http` may instead reject the request-target as a malformed percent-escape (HTTP 400) before it reaches the worker.
- **Reserved percent-decoding** — `%2F` becomes `/`, `%252F` stays `%2F`, `+` is not a space, `%3F` becomes `?` in the path only after the query is split off.

---

## Runtime errors

Workers print unhandled application exceptions to **stderr**, which Caddy inherits from the worker process. The HTTP client still receives a generic `500 Internal Server Error`; the traceback is not included in the response.

A typical log line looks like:

```text
Unhandled exception in ASGI HTTP handler (worker 0, GET /boom):
Traceback (most recent call last):
  ...
RuntimeError: ...
```

This is the same stream as import/syntax errors at worker start. If Caddy's own logs go to a file (`log { output file ... }`), Python tracebacks still appear on the **Caddy process stderr** (or whatever you redirected when you started `caddy run`).

**FastAPI / Starlette:** those frameworks send a 500 and then re-raise so the ASGI server can log. caddy-snake logs that exception even after the 500 has already been written.

Client disconnects mid-response are not treated as application errors and do not print a traceback.

---

## Limits to know

- Request bodies are **streamed** to the app in 64 KiB chunks with no worker-imposed size cap, so multi-gigabyte uploads work when the app reads incrementally (ASGI `receive()`, WSGI `wsgi.input.read(size)`, ESGI `iter_body()`). Apps that buffer the whole body (`UploadFile.read()` with no streaming, `wsgi.input.read()` with no size, ESGI `read_body()`) can still exhaust worker memory. Cap uploads on the `python` handler with [`request_body`](reference.md#request_body) (no wrapping `route` required):

```caddyfile
python {
    module_asgi "main:app"
    request_body {
        max_size 2GB
    }
}
```

- Joined WSGI responses are capped at **64 MiB** in the worker. Stream large downloads from ASGI instead.
- The [shared worker cache](reference.md#shared-worker-cache) is **not** a tenant boundary — prefix keys or use an external store.
- Dynamic app cache is bounded; large tenant counts need higher limits or external routing.
- Docker isolation hardens the worker sandbox; it does not isolate the shared cache.
