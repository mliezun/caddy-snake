---
title: ESGI
description: Event-based Server Gateway Interface with gevent workers
sidebar_position: 3
---

# ESGI

[ESGI](https://github.com/mliezun/esgi) (Event-based Server Gateway Interface) is a sync gateway for HTTP and WebSocket: one call per connection, a `scope`, and a blocking `protocol` object — no ASGI `receive`/`send` loop. Spec: [PROTOCOL.md](https://github.com/mliezun/esgi/blob/main/PROTOCOL.md) (`0.1-draft`).

## Config

`module_esgi` **always** runs a gevent worker (`runtime gevent`, the default). Install `gevent` in the app environment.

```caddyfile
python {
    module_esgi "main:application"
    runtime gevent
    venv "./venv"
    workers 4
}
```

Exactly one of `module_wsgi`, `module_asgi`, or `module_esgi` per block. Optional `__esgi_init__` / `__esgi_del__` hooks run at worker start/shutdown. Prefer `__esgi__(self, scope, protocol)` when present.

## Runtime defaults {#runtime-semantics}

| Interface | Allowed `runtime` | Default |
|-----------|-------------------|---------|
| WSGI | `sync`, `gevent` | `sync` |
| ESGI | `gevent` only | `gevent` |
| ASGI | `native`, `uvloop` | `uvloop` |

ASGI `uvloop` falls back to native asyncio if uvloop is not installed. Legacy `libuv` is normalized to `uvloop`.

## Coverage today

- **HTTP:** `read_body`, `iter_body`, `response_empty` / `response_str` / `response_bytes`, `response_file`, `response_file_range`
- **WebSocket:** `accept` → `WsTransport` (`receive` / `send_bytes` / `send_str`); RFC 6455 `close`
- **Not yet:** `response_stream` / full streaming refinements

Integration test: `./tests/integration.sh simple_esgi 3.13`.
