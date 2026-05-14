---
title: ESGI integration
description: ESGI gateway support with gevent-only workers and the runtime directive
sidebar_position: 3
---

# ESGI integration

[ESGI](https://github.com/mliezun/esgi) (**Event-based Server Gateway Interface**) is a Python gateway specification for HTTP and WebSocket: one application invocation per logical connection, a **`scope`** (metadata), and a **`protocol`** object with **blocking** read/write methods — without an ASGI-style `receive` / `send` loop. The normative draft lives in [**PROTOCOL.md**](https://github.com/mliezun/esgi/blob/main/PROTOCOL.md) (version **0.1-draft**).

## Gevent-only worker

**`module_esgi` always runs in a gevent worker.** There is **no asyncio** on the Python side for the connection to Caddy: the subprocess uses **`gevent.server.StreamServer`**, plain blocking sockets (cooperatively scheduled by gevent), and **greenlets** — e.g. one greenlet for the client connection and one to read WebSocket frames into a queue. Your app remains a **synchronous** `def application(scope, protocol): ...` (or `__esgi__`); it is never `async def` and is never driven by `asyncio`.

Install **`gevent`** in the same environment as your app (e.g. `venv` referenced from the Caddyfile).

## Configuration

Use **`module_esgi`** with the usual `module:variable` import path. Exactly one of `module_wsgi`, `module_asgi`, and `module_esgi` may be set.

**`runtime` must be `gevent`** for ESGI (this is also the default when `runtime` is omitted).

```caddyfile
python {
    module_esgi "main:application"
    runtime gevent
    venv "./venv"
    workers 4
}
```

Optional hooks from the spec: if the loaded app defines **`__esgi_init__`** / **`__esgi_del__`**, the worker calls them once at process startup and once at orderly shutdown.

Dispatch prefers **`__esgi__(self, scope, protocol)`** when present over a plain callable.

## Runtime semantics {#runtime-semantics}

| Directive | Values | Default (when `runtime` is omitted) |
| --- | --- | --- |
| `module_wsgi` | `sync`, `gevent` | `sync` |
| `module_esgi` | **`gevent`** only | `gevent` |
| `module_asgi` | `native`, `uvloop` | `uvloop` |

- **`native`** (ASGI): standard `asyncio` event loop (WSGI/ASGI worker subprocess).
- **`uvloop`** (ASGI): prefer **[uvloop](https://github.com/MagicStack/uvloop)** when installed; otherwise warn and fall back to native asyncio. (Legacy configs may say `libuv`; it is normalized to `uvloop`.)

## Protocol coverage

The current worker targets **0.1-draft** behavior used by real apps:

- **HTTP**: `read_body`, `iter_body`, `response_empty` / `response_str` / `response_bytes`, `response_file`, `response_file_range`, `wait_disconnect` (no-op until disconnect tracking lands).
- **WebSocket**: `accept` → `WsTransport` with `receive` / `send_bytes` / `send_str`; root `close` with RFC 6455 semantics.
- **`response_stream`** / full streaming refinements are not implemented yet (`NotImplementedError`).

## Integration tests

The repository includes **`tests/simple_esgi`**: a tiny ESGI app with HTTP and WebSocket echo checks (depends on **`gevent`**). Run it like other Docker integration targets:

```bash
./tests/integration.sh simple_esgi 3.13
```

## Roadmap

- Narrow **named exceptions** for oversized bodies and disconnects (spec open questions).
- **`response_stream`** and chunked/streaming parity with the draft.
- Optional **true gevent-based WSGI** mode (today `runtime gevent` for WSGI is still documented as thread-pool parity).

For ASGI/WSGI usage, see the main [configuration reference](reference).
