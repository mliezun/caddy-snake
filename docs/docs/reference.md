---
title: Configuration Reference
description: Complete reference of all configuration options for the Python directive
sidebar_position: 2
---

# Configuration Reference

The `python` directive allows you to serve Python WSGI or ASGI applications through Caddy. It supports both a simple form and a block form with various configuration options.

---

## Simple Form

The simple form allows you to specify a WSGI application using the `module:variable` pattern:

```caddyfile
python module_name:variable_name
```

For example:

```caddyfile
python myapp:app
```

This is equivalent to `module_wsgi` in the block form with a single worker.

---

## Block Form

The block form provides full configuration:

```caddyfile
python {
    module_wsgi <module_name:variable_name>
    module_asgi <module_name:variable_name>
    lifespan on|off
    working_dir <path>
    venv <path>
    workers <count>
    workers_runtime process|thread
    autoreload
}
```

---

## Subdirectives

### `module_wsgi`

Specifies a WSGI application using the `module:variable` pattern. The module is a Python module path (e.g. `main` for `main.py`, or `mysite.wsgi` for a Django project), and the variable is the WSGI callable within that module.

```caddyfile
python {
    module_wsgi "main:app"
}
```

You must specify either `module_wsgi` or `module_asgi`, but not both.

### `module_asgi`

Specifies an ASGI application using the `module:variable` pattern. Use this for async frameworks like FastAPI, Starlette, Django Channels, etc.

```caddyfile
python {
    module_asgi "main:app"
}
```

### `lifespan`

Controls the ASGI [lifespan protocol](https://asgi.readthedocs.io/en/latest/specs/lifespan.html) (`startup` and `shutdown` events). Only applicable when using `module_asgi`. Can be either `on` or `off`. Defaults to `off`.

```caddyfile
python {
    module_asgi "main:app"
    lifespan on
}
```

Enable this when your ASGI application uses startup/shutdown events (e.g. FastAPI's `@app.on_event("startup")` or the newer lifespan context manager).

### `working_dir`

Sets the working directory for the Python application. This affects:

- **Module resolution** — Python imports are resolved relative to this directory
- **Relative paths** — any relative paths in your app (e.g. for config files, static assets) are resolved from here
- **Consistent behavior** — ensures the same behavior across local development and production (e.g. when run under systemd, which defaults to `/`)

```caddyfile
python {
    module_wsgi "main:app"
    venv "/var/www/myapp/venv"
    working_dir "/var/www/myapp"
}
```

This is especially important in monorepo setups, containerized environments, or when running Caddy as a system service.

:::tip
When using `autoreload`, the working directory is also the root directory watched for `.py` file changes.
:::

The `working_dir` directive also supports [Caddy placeholders](#dynamic-module-loading) for dynamic resolution at request time.

### `venv`

Path to a Python virtual environment. Behind the scenes, this appends `venv/lib/python3.x/site-packages` to `sys.path` so installed packages are available to your app.

```caddyfile
python {
    module_wsgi "main:app"
    venv "/path/to/venv"
}
```

:::note
The venv packages are added to the global `sys.path`, which means all Python apps served by Caddy share the same packages.
:::

### `workers`

Number of worker processes (or threads) to spawn. Defaults to the number of CPUs (`GOMAXPROCS`).

```caddyfile
python {
    module_wsgi "main:app"
    workers 4
}
```

When using `workers_runtime thread`, only **1 worker** is used regardless of this setting, because all requests are handled within a single Python interpreter.

### `workers_runtime`

Controls how requests are handled concurrently:

| Value | Description |
|-------|-------------|
| `process` | **(default on Linux/macOS)** Spawns separate worker processes, each with its own Python interpreter. Best for CPU-bound workloads and production use. Provides true parallelism. |
| `thread` | Runs Python in the main Caddy process using a single interpreter. **Required on Windows.** Also required for `autoreload` and `dynamic module loading`. |

```caddyfile
python {
    module_wsgi "main:app"
    workers 4
    workers_runtime process
}
```

:::note
Process-based workers are not supported on Windows. Use `thread` instead.
:::

### `autoreload`

Watches the working directory for `.py` file changes and automatically reloads the Python app without restarting Caddy. Useful during development.

```caddyfile
python {
    module_wsgi "main:app"
    workers_runtime thread
    autoreload
}
```

**Requirements:**
- Requires `workers_runtime thread` since it reloads the Python module within the same interpreter.

**How it works:**
- Uses filesystem notifications (via [fsnotify](https://github.com/fsnotify/fsnotify)) to watch for `.py` file changes (write, create, remove, rename)
- Changes are debounced with a 500ms window to group rapid edits (e.g. editor save + format)
- The Python module cache (`sys.modules`) is invalidated for all modules in the working directory before reimporting
- The old app is cleaned up and a new one is created seamlessly
- In-flight requests complete before the swap happens (thread-safe with read/write locks)
- If the reload fails (e.g. syntax error), all requests return HTTP 500 until the next successful reload
- Hidden directories, `__pycache__`, and `node_modules` are automatically ignored

---

## Dynamic Module Loading

You can use [Caddy placeholders](https://caddyserver.com/docs/caddyfile/concepts#placeholders) in `module_wsgi`, `module_asgi`, `working_dir`, and `venv` to dynamically load different Python apps based on the request.

This is useful for multi-tenant setups where each subdomain or route serves a different application.

```caddyfile
*.example.com:9080 {
    route /* {
        python {
            module_asgi "{http.request.host.labels.2}:app"
            working_dir "{http.request.host.labels.2}/"
            workers_runtime thread
        }
    }
}
```

In this example:
- A request to `app1.example.com` loads the app from the `app1/` directory
- A request to `app2.example.com` loads the app from the `app2/` directory
- Apps are **lazily created** on first request and cached for subsequent requests

### How it works

When any of the configuration values (`module_wsgi`/`module_asgi`, `working_dir`, `venv`) contain Caddy placeholders (e.g. `{http.request.host.labels.2}`), Caddy Snake creates a **DynamicApp** that:

1. Resolves the placeholders at request time using the Caddy replacer
2. Builds a composite cache key from the resolved module, directory, and venv
3. Returns an existing app if one is cached for that key
4. Otherwise, lazily imports the Python module and creates a new app instance
5. Uses double-check locking for thread-safe concurrent access

### Dynamic modules + autoreload

Dynamic module loading works with `autoreload`. When enabled, each resolved working directory is independently watched for changes. When a `.py` file changes in a particular directory, only the apps associated with that directory are evicted from the cache and reimported on the next request.

```caddyfile
*.example.com:9080 {
    route /* {
        python {
            module_asgi "{http.request.host.labels.2}:app"
            working_dir "{http.request.host.labels.2}/"
            workers_runtime thread
            autoreload
        }
    }
}
```

Old app instances are cleaned up after a 10-second grace period to allow in-flight requests to complete safely.

---

## Notes

- You must specify either `module_wsgi` or `module_asgi`, but not both
- The `lifespan` directive is only used in ASGI mode
- When `working_dir` is specified, the path must exist and be a directory
- When specified, the `venv` path must point to a valid Python virtual environment
- `autoreload` requires `workers_runtime thread`
- Dynamic module loading (using placeholders) requires `workers_runtime thread`
