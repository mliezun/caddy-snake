---
title: Architecture
description: Technical details about how Caddy-Snake works, its worker model, autoreload, and dynamic modules
sidebar_position: 5
---

# Architecture

Caddy-Snake is a Caddy plugin that bridges the gap between Caddy's HTTP server and Python web applications. The Go plugin forwards HTTP requests to Python worker subprocesses, which speak WSGI, ASGI, or ESGI to your application.

---

## Request Flow

<img src="/en/latest/img/caddysnake-diagram.png" alt="Example request flow for Django app" width="600"></img>

1. Caddy receives an HTTP request
2. The request is routed to the Caddy-Snake plugin based on the Caddyfile configuration
3. The plugin selects a Python worker (round-robin across the worker pool)
4. The plugin forwards the HTTP request to the worker over a Unix domain socket (loopback TCP on Windows) using an internal reverse proxy
5. The worker ([`caddysnake.py`](https://github.com/mliezun/caddy-snake/blob/main/caddysnake.py)) translates the HTTP request into WSGI, ASGI, or ESGI protocol calls
6. The Python application processes the request and returns a response
7. The worker sends the HTTP response back to the plugin
8. Caddy sends the response to the client

---

## Python Integration

The plugin does not embed Python in the Caddy process. Instead, it spawns one or more **Python worker subprocesses** that run the bundled [`caddysnake.py`](https://github.com/mliezun/caddy-snake/blob/main/caddysnake.py) script. Each worker listens on a private Unix domain socket (or loopback TCP on Windows) and serves your WSGI, ASGI, or ESGI application.

Key aspects of the integration:
- **No CGO or Python C API** — the Go plugin and Python workers communicate over stream sockets using HTTP
- **Embedded worker script** — `caddysnake.py` is bundled into the plugin and written to a temporary directory at startup
- **Configurable Python binary** — workers use `python_path`, the venv interpreter, or `python3` from `PATH`
- **Pre-built standalone binaries** bundle a full Python distribution and pass its interpreter via `--python-path`

---

## Worker Model

Caddy-Snake uses process-based workers. Each worker runs in a separate OS process with its own Python interpreter:

- **True parallelism** — each process has its own GIL, so CPU-bound work runs in parallel
- **Isolation** — a crash in one worker doesn't affect others
- **Higher memory usage** — each process loads its own copy of the Python interpreter and application
- **Best for** — production deployments, CPU-bound workloads

---

## Autoreload

The autoreload feature provides hot-reloading during development without restarting Caddy. It is implemented in the `AutoreloadableApp` wrapper.

### How it works

1. A filesystem watcher ([fsnotify](https://github.com/fsnotify/fsnotify)) is started on the working directory, recursively watching all subdirectories
2. Only `.py` file events are considered (write, create, remove, rename)
3. Rapid changes are debounced with a 500ms window (e.g. when an editor saves and auto-formats)
4. When the debounce fires:
   - New worker subprocesses are started via the factory function
   - The new worker group replaces the old one atomically
   - Old worker processes are terminated after the swap
5. A read/write lock ensures in-flight requests complete before the swap
6. If the reload fails (e.g. syntax error in Python code), all subsequent requests return HTTP 500 until the next successful reload
7. If the app cannot be recreated at all (e.g. the working directory was deleted), the process terminates with exit code 1 to avoid silently serving errors indefinitely

### Thread safety

`AutoreloadableApp` uses `sync.RWMutex`:
- **Read lock** — held during request handling, allows concurrent requests
- **Write lock** — held during reload, blocks new requests until the swap is complete

---

## Dynamic Module Loading

Dynamic module loading allows a single Caddy configuration to serve multiple different Python applications, resolved at request time using [Caddy placeholders](https://caddyserver.com/docs/caddyfile/concepts#placeholders).

### How it works

The `DynamicApp` struct manages a cache of Python app instances keyed by their resolved configuration:

1. **Placeholder resolution** — on each request, Caddy placeholders in `module_wsgi`/`module_asgi`, `working_dir`, and `venv` are resolved using the request context (e.g. hostname, path, headers)
2. **Cache lookup** — a composite key (`module|dir|venv`) is used to look up an existing app
3. **Lazy creation** — if no app exists for the key, one is created via the factory function and cached
4. **Double-check locking** — a fast-path read lock allows concurrent access, with a write lock only for app creation

### Example: multi-tenant by subdomain

```caddyfile
*.example.com:9080 {
    route /* {
        python {
            module_asgi "{http.request.host.labels.2}:app"
            working_dir "{http.request.host.labels.2}/"
        }
    }
}
```

With this configuration:
- `app1.example.com` → imports `app1:app` from the `app1/` directory
- `app2.example.com` → imports `app2:app` from the `app2/` directory
- Each app is created on first request and reused for subsequent ones

### Dynamic modules + autoreload

When `autoreload` is enabled on a dynamic app, each resolved working directory gets its own filesystem watcher. File changes only affect the apps associated with that directory:

- A `dirToKeys` map tracks which cache keys belong to each working directory
- When a `.py` file changes, only the apps for that directory are evicted
- Old app instances are cleaned up after a 10-second grace period for in-flight requests
- The app is lazily reimported on the next request
- If the reimport fails on the next request, the process terminates (when `exitOnReloadFailure` is configured)

---

## WSGI Implementation

The WSGI implementation in [`caddysnake.py`](https://github.com/mliezun/caddy-snake/blob/main/caddysnake.py) follows the [PEP 3333](https://peps.python.org/pep-3333/) specification:

1. Parses the proxied HTTP request into WSGI components
2. Builds a WSGI environment dictionary with all required keys (`REQUEST_METHOD`, `PATH_INFO`, `QUERY_STRING`, headers, etc.)
3. Calls the WSGI application callable with the environment and a `start_response` callback
4. Collects the response status, headers, and body
5. Writes the HTTP response back to the Go plugin

## ASGI Implementation

The ASGI implementation in [`caddysnake.py`](https://github.com/mliezun/caddy-snake/blob/main/caddysnake.py) follows the [ASGI specification](https://asgi.readthedocs.io/en/latest/):

1. Parses the proxied HTTP request into an ASGI scope dictionary with connection details
2. Manages the ASGI protocol lifecycle via `receive` and `send` callables
3. Supports HTTP and WebSocket connections
4. Optionally handles the lifespan protocol for application startup/shutdown events

---

## Current Limitations

### Virtual Environment Sharing

When a `venv` is specified, the packages are added to the global `sys.path`. This means all Python apps served by the same Caddy instance have access to those packages, regardless of which app the venv was configured for.

---

## Worker Lifecycle

The plugin manages Python worker subprocesses from the Go side:

- Workers are started with `exec.Command` and the bundled `caddysnake.py` script
- Idle HTTP connections to workers are pooled via Go's `http.Transport`
- Workers receive SIGTERM on Unix for graceful shutdown (ASGI lifespan); Windows uses process kill
- Old worker groups are cleaned up during autoreload and dynamic module eviction
