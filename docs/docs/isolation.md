# Per-app isolation design

This document describes the `isolation` subdirective under the Caddy `python` handler.

## Goals

- **Hardening**: limit filesystem and environment exposure when a Python app is compromised.
- **Multi-tenant readiness**: separate worker units per app; per-tenant isolation config is handler-scoped in v1.
- **Pluggable backends**: Docker first; Firecracker and LXC can implement the same `WorkerBackend` interface.

## Threat model (v1)

| Boundary | `isolation none` (default) | `isolation docker` |
|----------|---------------------------|---------------------|
| Host filesystem | Full Caddy UID access | Bind-mounted `working_dir`, `venv`, worker script only |
| Host env | Inherits Caddy process env | `env_file` + `env_var` + internal `CADDYSNAKE_*` only |
| Other workers | Same UID | Separate containers |
| Shared cache | Per-handler in-process store | Reachable via `host.docker.internal`; **not** a tenant boundary |
| Network | Host network | Bridge + container IP dial from Caddy |

## Config

```caddyfile
python {
    module_wsgi "main:app"
    working_dir "/apps/site"
    workers 2
    isolation docker {
        image "python:3.13-slim"
    }
}
```

Omit `isolation` or use `isolation none` for the legacy subprocess model.

## Workers

`workers N` spawns **N isolated units** (N processes or N containers). Round-robin in `PythonWorkerGroup` is unchanged.

## Cache and IPC

When Docker isolation is enabled:

1. The in-process cache listens on TCP `127.0.0.1:<port>`.
2. Workers receive `CADDYSNAKE_CACHE_ADDR=host.docker.internal:<port>`.
3. Workers use TCP mode (`CADDYSNAKE_WORKER_TCP=1`, bind `0.0.0.0`) and write their listen port to a host-mounted port file.
4. Caddy dials the container IP and that port.

## Backend interface

Spawn logic lives behind `WorkerBackend` (`isolation.go`). `ProcessBackend` preserves existing behavior; `DockerBackend` uses the Docker CLI.

## Future backends

Firecracker and LXC should implement `WorkerBackend` without changing `AppServer` or `ServeHTTP`.
