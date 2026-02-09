---
title: Examples
description: Example implementations using different Python web frameworks
sidebar_position: 3
---

# Examples

This page provides example implementations using different Python web frameworks with Caddy-Snake. Each example includes the necessary Python code, Caddy configuration, and setup instructions.

---

## FastAPI (ASGI)

FastAPI is a modern, fast web framework for building APIs with Python.

```python
# main.py
from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()

class Item(BaseModel):
    name: str
    description: str | None = None
    price: float
    tax: float | None = None

@app.get("/")
def read_root():
    return {"Hello": "World"}

@app.get("/items/{item_id}")
def read_item(item_id: int):
    return {"item_id": item_id}

@app.post("/items/")
def create_item(item: Item):
    return item
```

```caddyfile
# Caddyfile
http://localhost:9080 {
    route {
        python {
            module_asgi "main:app"
            lifespan on
            workers 1
            workers_runtime thread
            venv "./venv"
        }
    }
}
```

Setup and run:

```bash
python -m venv venv
source venv/bin/activate
pip install fastapi
caddy run --config Caddyfile
```

Test:

```bash
curl http://localhost:9080/
curl http://localhost:9080/items/1
curl -X POST http://localhost:9080/items/ \
    -H "Content-Type: application/json" \
    -d '{"name":"test","price":10.5}'
```

---

## Flask (WSGI)

Flask is a lightweight web framework that's great for smaller applications.

```python
# main.py
from flask import Flask, jsonify, request

app = Flask(__name__)

@app.route("/")
def hello():
    return jsonify({"message": "Hello, World!"})

@app.route("/items/<int:item_id>")
def get_item(item_id):
    return jsonify({"item_id": item_id})

@app.route("/items", methods=["POST"])
def create_item():
    data = request.get_json()
    return jsonify(data), 201
```

```caddyfile
# Caddyfile
http://localhost:9080 {
    route {
        python {
            module_wsgi "main:app"
            workers 4
            workers_runtime process
            venv "./venv"
        }
    }
}
```

Setup and run:

```bash
python -m venv venv
source venv/bin/activate
pip install flask
caddy run --config Caddyfile
```

Test:

```bash
curl http://localhost:9080/
curl http://localhost:9080/items/1
curl -X POST http://localhost:9080/items \
    -H "Content-Type: application/json" \
    -d '{"name":"test"}'
```

---

## Django (WSGI)

Django is a full-featured web framework with a built-in admin interface and ORM.

```python
# mysite/settings.py
INSTALLED_APPS = [
    'django.contrib.admin',
    'django.contrib.auth',
    'django.contrib.contenttypes',
    'django.contrib.sessions',
    'django.contrib.messages',
    'django.contrib.staticfiles',
    'items',
]

# items/models.py
from django.db import models

class Item(models.Model):
    name = models.CharField(max_length=100)
    description = models.TextField(blank=True)
    created_at = models.DateTimeField(auto_now_add=True)

# items/views.py
from django.http import JsonResponse
from .models import Item

def item_list(request):
    items = Item.objects.all()
    data = [{"id": item.id, "name": item.name} for item in items]
    return JsonResponse(data, safe=False)
```

```caddyfile
# Caddyfile
http://localhost:9080 {
    route {
        python {
            module_wsgi "mysite.wsgi:application"
            workers 1
            workers_runtime thread
            venv "./venv"
        }
    }
}
```

Setup and run:

```bash
python -m venv venv
source venv/bin/activate
pip install django
django-admin startproject mysite .
python manage.py migrate
caddy run --config Caddyfile
```

Test:

```bash
curl http://localhost:9080/items/
```

---

## Django Channels (ASGI)

Django Channels extends Django with WebSocket support and other async protocols.

```python
# mysite/asgi.py
import os
from django.core.asgi import get_asgi_application

os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'mysite.settings')
application = get_asgi_application()
```

```caddyfile
# Caddyfile
http://localhost:9080 {
    route {
        python {
            module_asgi "mysite.asgi:application"
            workers 1
            venv "./venv"
        }
    }
}
```

Setup and run:

```bash
python -m venv venv
source venv/bin/activate
pip install django channels
caddy run --config Caddyfile
```

---

## Socket.IO

Socket.IO enables real-time, bidirectional communication. Here's a simple chat application:

```python
# main.py
from fastapi import FastAPI
from socketio import AsyncServer
from socketio.asgi import ASGIApp

app = FastAPI()
sio = AsyncServer(async_mode='asgi')
socket_app = ASGIApp(sio)

@sio.event
async def connect(sid, environ):
    print(f"Client connected: {sid}")

@sio.event
async def disconnect(sid):
    print(f"Client disconnected: {sid}")

@sio.event
async def message(sid, data):
    await sio.emit('message', data, skip_sid=sid)

app.mount('/', socket_app)
```

```caddyfile
# Caddyfile
http://localhost:9080 {
    route {
        python {
            module_asgi "main:app"
            lifespan on
            workers 1
            venv "./venv"
        }
    }
}
```

Setup and run:

```bash
python -m venv venv
source venv/bin/activate
pip install python-socketio fastapi
caddy run --config Caddyfile
```

Test with a WebSocket client:

```javascript
const socket = io('http://localhost:9080');
socket.on('connect', () => {
    console.log('Connected');
    socket.emit('message', 'Hello, World!');
});
socket.on('message', (data) => {
    console.log('Received:', data);
});
```

---

## Autoreload (Development)

Enable hot-reloading during development so your app reloads automatically when you edit Python files:

```python
# main.py
from flask import Flask

app = Flask(__name__)

@app.route("/")
def hello():
    return "Hello, World!"
```

```caddyfile
# Caddyfile
http://localhost:9080 {
    route {
        python {
            module_wsgi "main:app"
            workers_runtime thread
            venv "./venv"
            autoreload
        }
    }
}
```

Now when you edit `main.py`, the app reloads automatically — no need to restart Caddy. If you introduce a syntax error, requests will return HTTP 500 until you fix the code.

:::note
`autoreload` requires `workers_runtime thread`. Changes are debounced (500ms) to handle rapid edits.
:::

### Alternative: watchmedo

If you prefer to restart the entire Caddy process on file changes, you can use [watchmedo](https://github.com/gorakhargosh/watchdog?tab=readme-ov-file#shell-utilities):

```bash
# Install on Debian/Ubuntu
sudo apt-get install python3-watchdog

watchmedo auto-restart -d . -p "*.py" --recursive \
    -- caddy run --config Caddyfile
```

---

## Dynamic Module Loading (Multi-Tenant)

Serve multiple Python apps from a single Caddy configuration using Caddy placeholders. Each subdomain can load a different app.

### Project structure

```
project/
├── app1/
│   └── app1.py
├── app2/
│   └── app2.py
├── app3/
│   └── app3.py
└── Caddyfile
```

```python
# app1/app1.py
from fastapi import FastAPI

app = FastAPI()

@app.get("/")
def index():
    return {"app": "app1", "message": "Hello from App 1!"}
```

```python
# app2/app2.py
from fastapi import FastAPI

app = FastAPI()

@app.get("/")
def index():
    return {"app": "app2", "message": "Hello from App 2!"}
```

```caddyfile
# Caddyfile
*.127.0.0.1.nip.io:9080 {
    route /* {
        python {
            module_asgi "{http.request.host.labels.6}:app"
            working_dir "{http.request.host.labels.6}/"
            workers 1
            workers_runtime thread
        }
    }
}
```

Run:

```bash
pip install fastapi
caddy run --config Caddyfile
```

Test:

```bash
curl http://app1.127.0.0.1.nip.io:9080/
# {"app":"app1","message":"Hello from App 1!"}

curl http://app2.127.0.0.1.nip.io:9080/
# {"app":"app2","message":"Hello from App 2!"}
```

Each app is lazily loaded on first request and cached for subsequent requests.

### Dynamic modules + autoreload

Add `autoreload` to automatically reload individual apps when their Python files change:

```caddyfile
*.127.0.0.1.nip.io:9080 {
    route /* {
        python {
            module_asgi "{http.request.host.labels.6}:app"
            working_dir "{http.request.host.labels.6}/"
            workers 1
            workers_runtime thread
            autoreload
        }
    }
}
```

When you edit `app1/app1.py`, only `app1` is reloaded — `app2` and `app3` remain unaffected.

---

## Notes

- All examples assume you have Caddy-Snake installed. See [Installation & Distribution](installation.md) for all the ways to install it — including `pip install caddysnake` for the quickest setup
- The examples use different Caddy directives based on the framework:
  - `module_asgi` for FastAPI, Django Channels, and Socket.IO
  - `module_wsgi` for Flask and Django
  - `lifespan on` for ASGI applications that support startup/shutdown events
  - `working_dir` for Django to ensure proper module resolution
- Virtual environments are recommended for all examples
- Make sure to install all required dependencies before running the examples
- See the [Configuration Reference](reference.md) for full details on all directives
