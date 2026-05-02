## 🐍 caddy-snake v0.4.0

### Highlights

**Pure Python implementation  - CGo/C layer removed (#158)**
The entire WSGI/ASGI bridge has been rewritten from C (cgo) to pure Python, communicating with Go over lightweight worker processes. This eliminates the C compiler requirement at runtime, simplifies the build, and improves maintainability. The `workers_runtime` option has been removed  - multiprocess workers are now the only mode on all platforms.

**Standalone app binaries (embed-app)**
Package your Python app + Caddy + Python into a single self-contained executable  - similar to FrankenPHP. See the [embed-app docs](https://caddy-snake.readthedocs.io/en/latest/docs/embed-app/) for details.

**Benchmarks suite (#150)**
A new reproducible benchmark suite comparing caddy-snake against traditional reverse-proxy setups (Gunicorn/Uvicorn + Caddy). Results:

| Setup | Req/s | Avg Latency |
|---|---|---|
| Flask + Gunicorn + Caddy | 1,592 | 63.8 ms |
| **Flask + caddy-snake** | **3,782** | **26.4 ms** |
| FastAPI + Uvicorn + Caddy | 3,537 | 28.2 ms |
| **FastAPI + caddy-snake** | **5,730** | **17.4 ms** |

**Go 1.26 (#155)**
Upgraded to Go 1.26; minimum Python version raised to 3.12.

### Breaking changes

- **`workers_runtime` removed**  - the `process`/`thread` config option is gone. All platforms now use multiprocess workers.
- **`autoreload` no longer requires `workers_runtime thread`**  - just add `autoreload` directly.
- **Minimum Python raised to 3.12** (was 3.10).
- **Docker images** now ship Python 3.12, 3.13, and 3.14 only.
