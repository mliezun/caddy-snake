import sys

import socketio

# Create a Socket.IO server
sio = socketio.AsyncServer(
    async_mode='asgi',
    cors_allowed_origins=['http://localhost:9080'],
)

@sio.event
async def connect(sid, environ):
    print(f"User connected: {sid}", file=sys.stderr)

@sio.event
async def disconnect(sid):
    print(f"User disconnected: {sid}", file=sys.stderr)

@sio.event
async def start(sid, data):
    return {"sid": sid, "data": data}

@sio.event
async def ping(sid, data):
    await sio.emit("pong", {"sid": sid, "data": data})


app = socketio.ASGIApp(sio)
