---
title: Isolation
description: Run Python workers as host processes or Docker containers
sidebar_position: 4
---

# Isolation

By default each worker is a host subprocess with the same UID as Caddy. Use **`isolation docker`** when a compromised app should not see the full host filesystem or ambient environment.

## Quick config

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

Omit `isolation` or set `isolation none` for the default process model.

CLI: `--isolation docker --isolation-image python:3.13-slim` (plus optional `--isolation-network`, `--isolation-docker-host`, `--isolation-memory`, `--isolation-cpus`, `--isolation-read-only`).

Requires a working Docker engine (`docker` on `PATH`, access to the socket or `DOCKER_HOST`). Linux only.

## What changes

| Boundary | `isolation none` | `isolation docker` |
|----------|------------------|---------------------|
| Host filesystem | Full Caddy UID access | Bind-mounted `working_dir`, `venv`, worker script |
| Host env | Inherits Caddy env | `env_file` + `env_var` + internal `CADDYSNAKE_*` |
| Other workers | Same UID | Separate containers |
| Shared cache | In-process | Reachable via `host.docker.internal` — **still not a tenant boundary** |
| Network | Host network | Bridge; Caddy dials the container IP |

`workers N` → N processes or N containers. Round-robin is unchanged.

## When to use it

- Untrusted or multi-tenant code on one handler
- Preview environments where a branch should not read host files outside its release dir

When **not** enough: mutually hostile tenants that share the in-process cache, or need separate secrets stores — use key prefixes, external Redis, or separate Caddy handlers/hosts. For a shared-VM preview pattern, see [branch previews](../blog/branch-previews).

Full option list: [configuration reference](reference.md#isolation).
