---
title: Architecture
description: Technical details about how Caddy-Snake works, its worker model, autoreload, and dynamic modules
sidebar_position: 4
---

# Architecture

Caddy-Snake is a Caddy plugin that bridges the gap between Caddy's HTTP server and Python web applications. It translates HTTP requests into Python objects and communicates with WSGI or ASGI applications using their respective protocols.

---

## Request Flow

<img src="/en/latest/img/caddysnake-diagram.png" alt="Example request flow for Django app" width="600"></img>

1. Caddy receives an HTTP request
2. The request is routed to the Caddy-Snake plugin based on the Caddyfile configuration
3. The plugin translates the request into Python objects:
   - For WSGI: Creates a WSGI environment dictionary
   - For ASGI: Creates an ASGI scope dictionary
4. The request is dispatched to a worker (process or thread) running the Python application
5. The Python application processes the request and returns a response
6. The plugin translates the response back to HTTP
7. Caddy sends the response to the client

---

## Python Integration

The plugin uses Python's C API to embed a Python interpreter within the Caddy process. This integration is achieved through CGO, which allows Go code to call C functions and vice versa.

Key aspects of the integration:
- Python objects are created and managed via the C API (`PyObject`, `PyDict`, `PyList`, etc.)
- Reference counting is carefully managed to prevent memory leaks
- The GIL (Global Interpreter Lock) is properly acquired/released when crossing the Go/Python boundary
- Resources are cleaned up after each request

---

## Worker Model

Caddy-Snake supports two worker runtimes to handle concurrent requests:

### Process Workers (`workers_runtime process`)

The default on Linux and macOS. Each worker runs in a separate OS process with its own Python interpreter.

- **True parallelism** — each process has its own GIL, so CPU-bound work runs in parallel
- **Isolation** — a crash in one worker doesn't affect others
- **Higher memory usage** — each process loads its own copy of the Python interpreter and application
- **Best for** — production deployments, CPU-bound workloads

### Thread Workers (`workers_runtime thread`)

Runs Python in the main Caddy process using a single interpreter. The default (and only option) on Windows.

- **Lower memory** — single interpreter shared across requests
- **Simpler setup** — no IPC overhead
- **GIL-bound** — limited by Python's GIL for CPU-bound work (though I/O-bound apps perform well)
- **Required for** — `autoreload` and dynamic module loading
- **Best for** — development, I/O-bound workloads, dynamic multi-tenant setups

---

## Autoreload

The autoreload feature provides hot-reloading during development without restarting Caddy. It is implemented in the `AutoreloadableApp` wrapper.

### How it works

1. A filesystem watcher ([fsnotify](https://github.com/fsnotify/fsnotify)) is started on the working directory, recursively watching all subdirectories
2. Only `.py` file events are considered (write, create, remove, rename)
3. Hidden directories, `__pycache__`, and `node_modules` are automatically skipped
4. Rapid changes are debounced with a 500ms window (e.g. when an editor saves and auto-formats)
5. When the debounce fires:
   - The Python module cache (`sys.modules`) is invalidated for all modules under the working directory
   - The old application is cleaned up
   - The Python module is re-imported and a new application is created
   - The new app replaces the old one atomically
6. A read/write lock ensures in-flight requests complete before the swap
7. If the reload fails (e.g. syntax error in Python code), all subsequent requests return HTTP 500 until the next successful reload

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
            workers_runtime thread
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

---

## WSGI Implementation

The WSGI implementation follows the [PEP 3333](https://peps.python.org/pep-3333/) specification:

1. Creates a WSGI environment dictionary with all required keys (`REQUEST_METHOD`, `PATH_INFO`, `QUERY_STRING`, headers, etc.)
2. Calls the WSGI application callable with the environment and a `start_response` callback
3. Collects the response status, headers, and body
4. Writes the response back to the HTTP client via Caddy

## ASGI Implementation

The ASGI implementation follows the [ASGI specification](https://asgi.readthedocs.io/en/latest/):

1. Creates an ASGI scope dictionary with connection details
2. Manages the ASGI protocol lifecycle via `receive` and `send` callables
3. Supports HTTP and WebSocket connections
4. Optionally handles the lifespan protocol for application startup/shutdown events

---

## Current Limitations

### Shared Interpreter (Thread Mode)

When using `workers_runtime thread`, all Python applications share the same interpreter:

- No isolation between different applications
- Shared `sys.path` and `sys.modules`
- One application could potentially affect another's state
- Not suitable for running untrusted code

Process workers (`workers_runtime process`) provide better isolation since each worker has its own interpreter.

### Virtual Environment Sharing

When a `venv` is specified, the packages are added to the global `sys.path`. This means all Python apps served by the same Caddy instance have access to those packages, regardless of which app the venv was configured for.

---

## Memory Management

The plugin carefully manages memory between Go and Python:

- Uses CGO to safely pass data between Go and Python
- Properly handles Python object reference counting (`Py_INCREF`/`Py_DECREF`)
- Cleans up resources after each request
- Manages WebSocket connections and their lifecycle
- Old app instances are properly cleaned up during autoreload and dynamic module eviction
