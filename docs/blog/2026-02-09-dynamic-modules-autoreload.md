---
slug: dynamic-modules-autoreload
title: "Dynamic Modules & Autoreload: Serve Multiple Python Apps from One Caddy Instance"
authors: mliezun
tags: [python, caddy-snake, go, web, release]
---

This release introduces two major features: **dynamic module loading** and **built-in autoreload**. Together, they let you serve multiple Python apps from a single Caddy instance and hot-reload code changes during development — without restarting Caddy.

<!-- truncate -->

## Dynamic Module Loading

You can now use [Caddy placeholders](https://caddyserver.com/docs/caddyfile/concepts#placeholders) in `module_wsgi`, `module_asgi`, `working_dir`, and `venv` to dynamically resolve which Python app to load based on the incoming request.

This is perfect for multi-tenant setups where each subdomain (or route) serves a different application:

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
- `app1.example.com` → loads `app1/app1.py`
- `app2.example.com` → loads `app2/app2.py`
- `app3.example.com` → loads `app3/app3.py`

Apps are **lazily imported** on first request and cached for subsequent requests. There's no limit on how many apps you can serve — each one is created on demand.

Under the hood, `DynamicApp` resolves placeholders at request time, builds a composite cache key, and uses double-check locking for thread-safe concurrent access. This means the fast path (cache hit) only requires a read lock, keeping things efficient even under high concurrency.

## Built-in Autoreload

Previously, hot-reloading required an external tool like `watchmedo` to restart the entire Caddy process on file changes. Now, Caddy-Snake has a built-in `autoreload` directive that watches for `.py` file changes and reloads your app in-place:

```caddyfile
python {
    module_wsgi "main:app"
    autoreload
}
```

When you save a Python file, the app is automatically reloaded without restarting Caddy. Here's what happens behind the scenes:

1. A filesystem watcher (powered by [fsnotify](https://github.com/fsnotify/fsnotify)) monitors your working directory recursively
2. Changes are debounced with a 500ms window to handle rapid edits (e.g. editor save + auto-format)
3. The Python module cache (`sys.modules`) is invalidated for all modules under the working directory
4. The old app is cleaned up and a new one is imported
5. A read/write lock ensures in-flight requests complete before the swap

If the reload fails (e.g. syntax error), requests return HTTP 500 until the code is fixed.

## Dynamic Modules + Autoreload

The two features work together. When `autoreload` is enabled on a dynamic app, each resolved working directory gets its own filesystem watcher. Changes to `app1/` only reload `app1` — other apps remain unaffected:

```caddyfile
*.example.com:9080 {
    route /* {
        python {
            module_asgi "{http.request.host.labels.2}:app"
            working_dir "{http.request.host.labels.2}/"
            autoreload
        }
    }
}
```

Old app instances are cleaned up after a 10-second grace period to allow in-flight requests to complete safely.

## Getting Started

Install from PyPI for the quickest setup:

```bash
pip install caddysnake
```

Or update to the [latest release](https://github.com/mliezun/caddy-snake/releases) to download pre-built standalone binaries.

Check out the full documentation:

- [Installation & Distribution](../docs/installation) — all the ways to install Caddy Snake (PyPI, standalone binaries, Docker)
- [Configuration Reference](../docs/reference) — all directives explained in detail
- [Examples](../docs/examples) — working examples for dynamic modules, autoreload, and more
- [Architecture](../docs/architecture) — deep dive into how it all works
