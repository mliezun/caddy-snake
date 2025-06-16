---
title: Examples
description: Example implementations using different Python web frameworks
---

# Examples

This page provides example implementations using different Python web frameworks with Caddy-Snake. Each example includes the necessary Python code, Caddy configuration, and setup instructions.

## FastAPI Example

FastAPI is a modern, fast web framework for building APIs with Python. Here's a simple example:

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
        }
    }
}
```

To run this example:

1. Install dependencies:
```bash
pip install fastapi uvicorn
```

2. Start Caddy:
```bash
caddy run --config Caddyfile
```

3. Test the API:
```bash
curl http://localhost:9080/
curl http://localhost:9080/items/1
curl -X POST http://localhost:9080/items/ -H "Content-Type: application/json" -d '{"name":"test","price":10.5}'
```

## Flask Example

Flask is a lightweight web framework that's great for smaller applications. Here's a basic example:

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
        }
    }
}
```

To run this example:

1. Install dependencies:
```bash
pip install flask
```

2. Start Caddy:
```bash
caddy run --config Caddyfile
```

3. Test the API:
```bash
curl http://localhost:9080/
curl http://localhost:9080/items/1
curl -X POST http://localhost:9080/items -H "Content-Type: application/json" -d '{"name":"test"}'
```

## Django Example

Django is a full-featured web framework with a built-in admin interface. Here's a basic example:

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
            working_dir "."
        }
    }
}
```

To run this example:

1. Install dependencies:
```bash
pip install django
```

2. Set up the database:
```bash
python manage.py migrate
```

3. Start Caddy:
```bash
caddy run --config Caddyfile
```

4. Test the API:
```bash
curl http://localhost:9080/items/
```

## Socket.IO Example

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
        }
    }
}
```

To run this example:

1. Install dependencies:
```bash
pip install python-socketio fastapi
```

2. Start Caddy:
```bash
caddy run --config Caddyfile
```

3. Test with a WebSocket client:
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

## Notes

- All examples assume you have Caddy-Snake installed and configured
- The examples use different Caddy directives based on the framework:
  - `module_asgi` for FastAPI and Socket.IO
  - `module_wsgi` for Flask and Django
  - `lifespan on` for ASGI applications that support it
  - `working_dir` for Django to ensure proper module resolution
- Virtual environments are recommended for all examples
- Make sure to install all required dependencies before running the examples 