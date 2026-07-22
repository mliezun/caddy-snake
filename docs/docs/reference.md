---
title: Configuration Reference
description: Complete reference of all configuration options for the Python directive
sidebar_position: 2
---

# Configuration Reference

The `python` directive allows you to serve Python WSGI, ASGI, or ESGI applications through Caddy. It supports a simple form, a block form, and path-prefixed route blocks.

---

## Route Form (recommended)

Mount a Python app on a URL path directly, without wrapping it in a `route` block:

```caddyfile
python "/api" {
    module_asgi "main:app"
}
```

The optional matcher can be a path (`/api`, `/api/*`), a named matcher (`@api`), or a catch-all (`*`). Requests are forwarded to Python with the **full request path** unchanged.

For a catch-all site:

```caddyfile
python /* {
    module_asgi "main:app"
}
```

The older `route { python { ... } }` form remains supported but is no longer recommended.

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
    module_esgi <module_name:variable_name>
    runtime <sync|gevent|native|uvloop>
    lifespan on|off
    working_dir <path>
    venv <path>
    env_file <path>
    env_var <name> <value>
    workers <count>
    start_timeout <duration|-1|forever>
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

You must specify exactly one of `module_wsgi`, `module_asgi`, or `module_esgi`.

### `module_asgi`

Specifies an ASGI application using the `module:variable` pattern. Use this for async frameworks like FastAPI, Starlette, Django Channels, etc.

```caddyfile
python {
    module_asgi "main:app"
}
```

### `module_esgi`

Specifies an [ESGI](esgi) application using the `module:variable` pattern (a synchronous `application(scope, protocol)` or `__esgi__` callable).

```caddyfile
python {
    module_esgi "main:application"
    runtime gevent
}
```

### `runtime`

Selects how the Python worker runs your app at the gateway boundary. See [ESGI integration: runtime semantics](esgi#runtime-semantics) for details.

- With **`module_wsgi`**: `sync` (default) or `gevent`.
- With **`module_esgi`**: **`gevent` only** (default).
- With **`module_asgi`**: `native` or `uvloop` (default when omitted: `uvloop`).

```caddyfile
python {
    module_asgi "main:app"
    runtime native
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

### `env_file`

Path to a dotenv-style file (for example `.env`) loaded into the Python worker environment for this `python` block. Repeat the directive to load multiple files; later files override earlier ones for duplicate keys.

Relative paths are resolved against `working_dir` when set, otherwise against the Caddy process working directory.

```caddyfile
python {
    module_wsgi "main:app"
    working_dir "/var/www/myapp"
    env_file "/var/www/myapp/.env"
}
```

Supported file format:

- `KEY=VALUE` lines
- Optional double or single quotes around values
- `#` comments and blank lines
- Optional `export KEY=VALUE` prefix

Changes to env files are picked up when workers are restarted or when [autoreload](#autoreload) respawns workers; env files are not watched independently.

### `env_var`

Set an individual environment variable for workers in this `python` block. Specify exactly two arguments: the variable name and value. Repeat to set multiple variables; later lines override earlier ones for the same name.

**`env_var` is applied after `env_file`**, so inline values override file values when both define the same key.

```caddyfile
python {
    module_asgi "main:app"
    working_dir "/var/www/myapp"
    env_file "/var/www/myapp/.env"
    env_var DEBUG "1"
    env_var DATABASE_URL "postgres://localhost/dev"
}
```

Variable names must match `[A-Za-z_][A-Za-z0-9_]*`. Reserved names (`PYTHONUNBUFFERED`, `CADDYSNAKE_*`) cannot be set from the Caddyfile.

**Environment precedence:** Caddy process env → `env_file` → `env_var` → internal worker vars (`PYTHONUNBUFFERED`, `CADDYSNAKE_*`).

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

Number of worker processes to spawn. Defaults to the number of CPUs (`GOMAXPROCS`).

```caddyfile
python {
    module_wsgi "main:app"
    workers 4
}
```

### `start_timeout`

How long Caddy waits for each Python worker to become ready (Unix socket or Windows port file) during provisioning. Optional; defaults to **`120s`**.

Use a Caddy duration (`30s`, `2m`, …) or **`-1`** / **`forever`** to wait indefinitely until the worker is ready or the process exits.

If the configured timeout is longer than `120s` (including `-1` / `forever`) and the app is still loading after 120 seconds, Caddy logs a warning and keeps waiting.

On the CLI, pass indefinite wait as `--start-timeout=-1` (equals form) or `--start-timeout forever`. A bare `--start-timeout -1` is rejected by Cobra/pflag because `-1` looks like a flag name.

```caddyfile
python {
    module_wsgi "mysite.wsgi:application"
    working_dir "/var/www/myapp"
    venv "/var/www/myapp/venv"
    start_timeout 180s
}
```

Workers that exit before becoming ready fail immediately with an error (the full timeout is not consumed).

### `autoreload`

Watches the working directory for `.py` file changes and automatically reloads the Python app without restarting Caddy. Useful during development.

```caddyfile
python {
    module_wsgi "main:app"
    autoreload
}
```

**How it works:**
- Uses filesystem notifications (via [fsnotify](https://github.com/fsnotify/fsnotify)) to watch for `.py` file changes (write, create, remove, rename)
- Changes are debounced with a 500ms window to group rapid edits (e.g. editor save + format)
- The Python module cache (`sys.modules`) is invalidated for all modules in the working directory before reimporting
- The old app is cleaned up and a new one is created seamlessly
- In-flight requests complete before the swap happens (thread-safe with read/write locks)
- If the reload fails (e.g. syntax error in Python code), the app degrades to returning HTTP 500 for all requests until the next file change triggers a successful reload
- If the app cannot be loaded at all (e.g. app directory deleted), the Caddy process terminates to avoid silently serving errors

---

## Dynamic Module Loading

You can use [Caddy placeholders](https://caddyserver.com/docs/caddyfile/concepts#placeholders) in `module_wsgi`, `module_asgi`, `working_dir`, `venv`, `env_file`, and `env_var` values to dynamically load different Python apps based on the request.

This is useful for multi-tenant setups where each subdomain or route serves a different application.

```caddyfile
*.example.com:9080 {
    python /* {
        module_asgi "{http.request.host.labels.2}:app"
        working_dir "{http.request.host.labels.2}/"
    }
}
```

In this example:
- A request to `app1.example.com` loads the app from the `app1/` directory
- A request to `app2.example.com` loads the app from the `app2/` directory
- Apps are **lazily created** on first request and cached for subsequent requests

### How it works

When any of the configuration values (`module_wsgi`/`module_asgi`, `working_dir`, `venv`, `env_file`, or `env_var` values) contain Caddy placeholders (e.g. `{http.request.host.labels.2}`), Caddy Snake creates a **DynamicApp** that:

1. Resolves the placeholders at request time using the Caddy replacer
2. Builds a composite cache key from the resolved module, directory, venv, env files, and inline env vars
3. Returns an existing app if one is cached for that key
4. Otherwise, lazily imports the Python module and creates a new app instance
5. Uses double-check locking for thread-safe concurrent access

### Dynamic modules + autoreload

Dynamic module loading works with `autoreload`. When enabled, each resolved working directory is independently watched for changes. When a `.py` file changes in a particular directory, only the apps associated with that directory are evicted from the cache and reimported on the next request.

```caddyfile
*.example.com:9080 {
    python /* {
        module_asgi "{http.request.host.labels.2}:app"
        working_dir "{http.request.host.labels.2}/"
        autoreload
    }
}
```

Old app instances are cleaned up after a 10-second grace period to allow in-flight requests to complete safely.

---

## On-demand TLS (certificate permission without `ask`)

When you serve many HTTPS hostnames under one zone (for example `{branch}.project.example`), Caddy normally needs to know each name for automatic HTTPS, **or** you use [On-Demand TLS](https://caddyserver.com/docs/caddyfile/options#on-demand-tls). On-demand issuance **must** be gated by permission: either an HTTP **`ask`** URL or a **`tls.permission.*`** module. Caddy Snake ships **`tls.permission.python_dir`** so you can **avoid running a separate `ask`** service: it allows a certificate only if the hostname looks like **`{slug}.{your_domain_suffix}`** and **`{root}/{slug}`** exists as a directory.

### Pairing `python_dir` with dynamic Python

Use the **`python_dir` `root`** (and slug) naming as **`working_dir`**. **[Host labels](https://caddyserver.com/docs/caddyfile/concepts#placeholders)** are numbered from **the right**: for **`featureb.project.example`**, **`{http.request.host.labels.2}`** is **`featureb`**.

```caddyfile
{
	on_demand_tls {
		permission python_dir {
			root /srv/branches
			domain_suffix project.example
		}
	}
}

https://*.project.example {
	tls {
		on_demand
	}
	python /* {
		module_asgi "{http.request.host.labels.2}:app"
		working_dir "/srv/branches/{http.request.host.labels.2}/"
	}
}
```

If you want a wildcard-style site (`*.project.example`), you can combine that pattern with matchers as usual.

### nip.io with embedded IPv4 (one wildcard site, many HTTPS apps) {#nip-io-https-many-apps}

[nip.io](https://nip.io/) resolves hostnames that embed your public IPv4 in dotted quad form before `.nip.io`, e.g. **`app7.203.0.113.43.nip.io`** → `203.0.113.43`. That hostname has **seven** labels (`app7`, four octets, `nip`, `io`). Caddy [`http.request.host.labels.N`](https://caddyserver.com/docs/caddyfile/concepts#placeholders) counts **from the right**, so the leftmost slug (`app7`) is **`{http.request.host.labels.6}`**.

Use the **same suffix** for TLS permission and for DNS:

- **`domain_suffix`** (no leading dot): **`203.0.113.43.nip.io`** — substitute **`203.0.113.43`** with your server’s real public IPv4 (the example uses [RFC 5737 TEST-NET-3](https://datatracker.ietf.org/doc/html/rfc5737) documentation space only as illustration).
- **`root`**: base directory with one subdirectory per slug (`app1`, `app2`, …).

Put **`on_demand_tls`** + **`permission python_dir`** in global options, expose **one** HTTPS site **`https://*.{your-ipv4}.nip.io`** with **`tls { on_demand }`**, and point **`working_dir`** at **`/srv/apps/{http.request.host.labels.6}/`**. Each slug gets HTTPS only if **`python_dir`** allows it (directory exists); unknown slugs should **not** obtain a certificate.

Fixed **`module_wsgi`** with per-request **`working_dir`** (every app exposes `application` in its own `app.py`):

```caddyfile
{
	email you@your-domain.example

	on_demand_tls {
		permission python_dir {
			root /srv/apps
			domain_suffix 203.0.113.43.nip.io
		}
	}
}

https://*.203.0.113.43.nip.io {
	tls {
		on_demand
	}

	python /* {
		module_wsgi "app:application"
		working_dir "/srv/apps/{http.request.host.labels.6}/"
		workers 2
	}
}
```

:::note ACME account email

Let's Encrypt rejects registration contacts at **`example.com`** and under **`.invalid`**. Use a normal mailbox on a registrable domain. For throwaway demos some operators use **`admin@nip.io`** because **`nip.io`** is a real zone — prefer your own domain for anything serious.

:::

Smoke-test many apps:

```bash
for i in $(seq 1 10); do curl -fsS "https://app${i}.203.0.113.43.nip.io/"; echo; done
```

(Again, replace `203.0.113.43` with your live IPv4.)

### Directive reference (`tls.permission.python_dir`)

- **`root`** — Base directory containing one subdirectory per slug (deploy path).
- **`domain_suffix`** — The registered suffix (without leading dot): hostname must be exactly **`{slug}.` plus this suffix (**one** label before it).

DNS must point `*.yourzone` at the server, and **`email`** should be set in global options for ACME accounts when using public issuance.

For **automated CI-style runs** without a public CA (internal issuer + on-demand TLS + `permission python_dir`), build Caddy Snake with xcaddy so the **`tls`** app loads the **`python_dir`** plugin, then use the **`caddytest`**-tagged HTTPS test:

```bash
go test -race -timeout 120s -tags=caddytest . \
  -run TestPythonDir_OnDemandDynamicASGI_OverHTTPS -v
```

That test requires **Python 3** on `PATH`.

---

## Shared worker cache {#shared-worker-cache}

When Caddy Snake runs **Python in process worker mode** (the default multi-process layout), it may start a small **in-process cache server** inside the **Go plugin**. Worker processes talk to it over a **stream socket** using a **RESP2**-shaped line protocol (Redis-compatible enough for simple clients):

- **Linux and macOS:** the listener is a **Unix domain socket** in a private temporary directory. The **`CADDYSNAKE_CACHE_ADDR`** environment variable is set to **`unix:///absolute/path/to/cache.sock`** (three slashes after the scheme: `unix://` plus an absolute path).
- **Windows:** the listener is **TCP on loopback**; **`CADDYSNAKE_CACHE_ADDR`** is **`127.0.0.1:<port>`**.

Caddy sets these automatically for eligible workers:

| Variable | Purpose |
| --- | --- |
| **`CADDYSNAKE_CACHE_ADDR`** | Socket path (`unix://…`) or `host:port` for the cache |
| **`CADDYSNAKE_WORKER_INTERFACE`** | Worker kind (`wsgi`, `asgi`, `esgi`, …); selects compatible client socket APIs (e.g. gevent for ESGI) |
| **`CADDYSNAKE_WORKER_ID`** | Stable worker index **`0`…`N-1`** within the worker group (reused after reload; combine with conn/generation in app keys) |
| **`CADDYSNAKE_CACHE_TIMEOUT`** | Hint for client read/connect timeouts (seconds) |

If **`CADDYSNAKE_CACHE_ADDR`** is unset, the cache client is not available (for example, thread workers or configurations that omit the shared cache).

### How values are stored

Each key is in one of three shapes:

| Shape | How it is created | What `get` returns |
| --- | --- | --- |
| **Scalar** | `set(key, value)` | `bytes` |
| **List** (FIFO) | `append` on a missing key, or on a scalar (scalar becomes first element) | `list[bytes]` (may be empty) |
| **Set** | `sadd(key, member)` | Use **`smembers`** — `get` returns an error for set keys |

- **`set`** replaces whatever was there (scalar, list, or set) with a new scalar. Optional **`ttl`** is in whole **seconds**; omit for no expiry until delete/overwrite.
- **`get`** returns `None` if the key is missing or expired.
- **`delete`** returns `1` if a key was removed, `0` if nothing was stored under that key.
- **`append`** appends one chunk to the list. If the key held a scalar, the value becomes `[old_scalar, new_chunk]`. **TTL is cleared** when working with list data.
- **`pop`** removes the **first** list element (FIFO). It returns `None` if the key is missing, expired, holds a **scalar** or **set**, or the list is empty when **`timeout`** is omitted. With **`timeout=float(seconds)`**, the server waits up to that long for another worker to **`append`**.
- **`sadd` / `srem` / `smembers`** manage **set** membership (unique members). **`smembers`** returns an empty list for a missing key. Remove stale members with **`srem`** on disconnect (sets have no per-member TTL).
- **`setnx(key, value, ttl=None)`** sets a scalar only if the key is absent; returns **`True`** if set, **`False`** if another value exists.
- **`keys(prefix, limit=1000)`** lists key names (as **`bytes`**) matching a **non-empty** prefix (hard cap **1000**). All workers share one cache — **prefix keys** (e.g. `myapp:group:foo`) to avoid collisions.
- **`publish(channel, message)`** delivers to workers **currently blocked** on **`subscribe(channel, timeout)`** (one-shot blocking receive, not persistent Redis-style pub/sub). Returns the number of waiters notified.
- **`subscribe(channel, timeout)`** requires a **positive timeout** (seconds). Returns **`bytes`** or **`None`** on timeout.

:::note Security and limitations

There is **no tenant isolation** between workers or dynamic apps on the same handler — any worker can read, delete, or enumerate keys. Use app-specific prefixes. **`CSGROUPSEND`** (atomic set fan-out) is not built in; use **`smembers`** + **`append`** in app code with known race trade-offs, or external Redis for full channel-layer semantics.

:::

### Wire protocol (CS* commands)

All commands are RESP2 arrays of bulk strings. Replies are bulk (`$…`), integer (`:…`), array (`*…`), simple string (`+OK`), or error (`-ERR …`).

| Command | Arguments | Reply | Notes |
| --- | --- | --- | --- |
| **`CSGET`** | key | bulk, array, or `$-1` | Set keys → `-ERR wrong type` |
| **`CSSET`** | key value [ttl_sec] | `+OK` | Overwrites any prior type |
| **`CSDEL`** | key | `:0` / `:1` | |
| **`CSAPPEND`** | key chunk | `+OK` | List only; wrong type on sets |
| **`CSPOP`** | key [timeout_sec] | bulk or `$-1` | FIFO; blocks when timeout set |
| **`CSSADD`** | key member | `:0` / `:1` | Creates set if missing |
| **`CSSREM`** | key member | `:0` / `:1` | Deletes key when last member removed |
| **`CSSMEMBERS`** | key | `*N` bulks | `*0` if missing (Redis-aligned) |
| **`CSSETNX`** | key value [ttl_sec] | `:0` / `:1` | Scalar only if absent |
| **`CSKEYS`** | prefix [limit] | `*N` bulks | **Prefix required**; limit ≤ 1000 |
| **`CSPUBLISH`** | channel message | `:N` | `N` = waiters notified |
| **`CSSUBSCRIBE`** | channel timeout_sec | bulk or `$-1` | Timeout required; max **300** s |
| **`CSQUIT`** | — | `+OK` | Closes connection |

Blocking **`CSPOP`** and **`CSSUBSCRIBE`** hold a server goroutine until data arrives, timeout, or cache shutdown (Caddy reload).

### Python API

Install the **`caddysnake`** Python package (same as the CLI). Import the module-level singleton (or instantiate `Cache`):

```python
from caddysnake import cache
```

#### `cache.set(key, value, ttl=None)`

Store a scalar. Overwrites any prior value.

```python
cache.set("config:theme", b"dark")
cache.set("session:abc", serialized, ttl=3600)  # expire after 1 hour (server-side)
```

#### `cache.get(key)`

Return `bytes` (scalar), `list[bytes]` (list), or `None`.

```python
raw = cache.get("config:theme")
if raw is None:
    ...
queue = cache.get("events")
if isinstance(queue, list):
    for chunk in queue:
        ...
```

#### `cache.delete(key) -> int`

```python
if cache.delete("session:abc"):
    ...
```

#### `cache.append(key, chunk)`

Build or grow a list. Typical pattern: **job queue** chunks or log lines.

```python
cache.append("jobs", b"task-1")
cache.append("jobs", b"task-2")
```

#### `cache.pop(key, timeout=None)`

FIFO pop for **list** keys only.

```python
# Non-blocking: returns None if the list is empty
item = cache.pop("jobs")

# Wait up to 30s for another worker to append
item = cache.pop("jobs", timeout=30.0)
```

#### `cache.aset` / `cache.aget` / … / `cache.apop`

Async variants for ASGI: each call runs the blocking client in `asyncio.to_thread`.

#### `cache.sadd` / `cache.srem` / `cache.smembers`

Set operations for group rosters and connection registries.

#### `cache.setnx(key, value, ttl=None) -> bool`

Atomic insert-if-absent (scalar only).

#### `cache.keys(prefix, limit=1000) -> list[bytes]`

Prefix key listing (admin/debug; **`prefix` required**).

#### `cache.publish` / `cache.subscribe`

One-shot blocking fan-out: **`subscribe`** must pass **`timeout`** (seconds).

#### `worker_id() -> int | None`

Reads **`CADDYSNAKE_WORKER_ID`** (`0`…`N-1`) when set.

```python
from caddysnake import cache, worker_id

await cache.aset("k", b"v")
val = await cache.aget("k")
await cache.aappend("q", b"work")
item = await cache.apop("q", timeout=5.0)
cache.sadd("group:room", f"worker:{worker_id()}".encode())
msg = cache.subscribe("events", timeout=10.0)
```

:::note

`CacheError` is raised when the server returns an error line or the connection fails. `CacheConfigurationError` (a subclass) means `CADDYSNAKE_CACHE_ADDR` is missing or invalid — for example, code was not started under Caddy process workers with the shared cache enabled.

:::

`cache` is a thin façade; you can use the **`Cache`** class directly if you prefer. ESGI workers need **`gevent`** installed (TCP cache client path on Windows); Unix cache paths use the standard library `socket` for `unix://` addresses.

:::note

This cache is **ephemeral** and **not** a substitute for Redis or a database: it is scoped to the Caddy process, subject to memory limits, and intended for small shared objects or coordination between workers. Prefer an external store for durability or large payloads.

:::

---

## `python-server` command

The `caddy python-server` command (and the PyPI `caddysnake` wrapper) exposes the same Python-handler settings as the [`python` block](#block-form), plus a few CLI-only conveniences for listen address, HTTPS, and static files.

```bash
caddy python-server --server-type asgi --app main:app \
  --working-dir /var/www/myapp \
  --venv /var/www/myapp/venv \
  --env-file /var/www/myapp/.env \
  --env-var DEBUG=1 \
  --start-timeout 180s \
  --workers 4
```

| Caddyfile | CLI flag |
|-----------|----------|
| `module_wsgi` / `module_asgi` / `module_esgi` | `--server-type` + `--app` |
| `runtime` | `--runtime` |
| `lifespan` | `--lifespan` |
| `working_dir` | `--working-dir` |
| `venv` | `--venv` |
| `workers` | `--workers` |
| `start_timeout` | `--start-timeout` (use `--start-timeout=-1` or `forever` for indefinite) |
| `autoreload` | `--autoreload` |
| `python_path` | `--python-path` |
| `env_file` | `--env-file` (repeatable) |
| `env_var <name> <value>` | `--env-var NAME=VALUE` (repeatable) |

CLI-only: `--domain`, `--listen`, `--static-path`, `--static-route`, `--debug`, `--access-logs`.

---

## Notes

- You must specify exactly one of `module_wsgi`, `module_asgi`, or `module_esgi`
- The `lifespan` directive is only used in ASGI mode
- When `working_dir` is specified, the path must exist and be a directory
- When specified, the `venv` path must point to a valid Python virtual environment
