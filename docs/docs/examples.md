---
title: Examples
description: Flask, FastAPI, Django, Socket.IO, autoreload, and multi-tenant setups
sidebar_position: 3
---

# Examples

Short, copy-pasteable recipes. Install with [PyPI](installation.md) or any other method, then run `caddy run --config Caddyfile` (or `caddysnake` with equivalent flags).

---

## FastAPI (ASGI)

```python
# main.py
from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()

class Item(BaseModel):
    name: str
    price: float

@app.get("/")
def read_root():
    return {"Hello": "World"}

@app.post("/items/")
def create_item(item: Item):
    return item
```

```caddyfile
http://localhost:9080 {
    python /* {
        module_asgi "main:app"
        lifespan on
        venv "./venv"
    }
}
```

```bash
python -m venv venv && source venv/bin/activate
pip install fastapi
caddy run --config Caddyfile
```

---

## Flask (WSGI)

```python
# main.py
from flask import Flask, jsonify

app = Flask(__name__)

@app.route("/")
def hello():
    return jsonify({"message": "Hello, World!"})
```

```caddyfile
http://localhost:9080 {
    python /* {
        module_wsgi "main:app"
        workers 4
        venv "./venv"
    }
}
```

---

## Django (WSGI)

```caddyfile
http://localhost:9080 {
    python /* {
        module_wsgi "mysite.wsgi:application"
        working_dir "."
        venv "./venv"
    }
}
```

```bash
pip install django
django-admin startproject mysite .
python manage.py migrate
caddy run --config Caddyfile
```

For Django Channels / ASGI, swap to `module_asgi "mysite.asgi:application"`.

---

## Socket.IO

```python
# main.py
from fastapi import FastAPI
from socketio import AsyncServer
from socketio.asgi import ASGIApp

app = FastAPI()
sio = AsyncServer(async_mode="asgi")
app.mount("/", ASGIApp(sio))

@sio.event
async def message(sid, data):
    await sio.emit("message", data, skip_sid=sid)
```

```caddyfile
http://localhost:9080 {
    python /* {
        module_asgi "main:app"
        lifespan on
        venv "./venv"
    }
}
```

---

## Environment variables

```caddyfile
python {
    module_asgi "shop:app"
    working_dir "/apps/shop"
    env_file "/apps/shop/.env"
    env_var APP_NAME "shop"
}
```

`env_var` overrides `env_file` for the same key. Placeholders work in values for dynamic apps.

---

## Autoreload

```caddyfile
python /* {
    module_wsgi "main:app"
    venv "./venv"
    autoreload
}
```

Edits to `.py` files reload the app in place (500ms debounce). Syntax errors → HTTP 503 until fixed.

Avoid autoreload behind long-lived production WebSockets; see [Architecture](architecture.md#autoreload).

---

## Multi-tenant / branch hosts

```
project/
├── app1/app1.py
├── app2/app2.py
└── Caddyfile
```

```caddyfile
*.127.0.0.1.nip.io:9080 {
    python /* {
        module_asgi "{http.request.host.labels.6}:app"
        working_dir "{http.request.host.labels.6}/"
        autoreload
    }
}
```

```bash
curl http://app1.127.0.0.1.nip.io:9080/
curl http://app2.127.0.0.1.nip.io:9080/
```

Each hostname resolves its own app on first request (cached; default max 128). For real HTTPS on a VPS IP via nip.io, use on-demand TLS + `permission python_dir` — see the [reference](reference.md#nip-io-https-many-apps).

For a production-shaped preview setup (release dirs + DB clones), read [Branch previews on one box](../blog/branch-previews).
