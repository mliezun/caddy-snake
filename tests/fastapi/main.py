import random
import sys
from typing import Optional
from contextlib import asynccontextmanager

from fastapi import FastAPI, WebSocket
from fastapi.responses import StreamingResponse, HTMLResponse
from pydantic import BaseModel


@asynccontextmanager
async def lifespan(app: FastAPI):
    print("Lifespan startup", file=sys.stderr)
    yield
    print("Lifespan shutdown", file=sys.stderr)


app = FastAPI(lifespan=lifespan)

db = {}


class Item(BaseModel):
    name: str
    description: str
    blob: Optional[str]


@app.get("/item/{id}")
async def get_item(id: str):
    return db.get(id)


@app.post("/item/{id}")
async def store_item(id: str, item: Item):
    db[id] = item
    return "Stored"


@app.delete("/item/{id}")
async def delete_item(id: str):
    del db[id]
    return "Deleted"


def chunked_blob(blob: str):
    chunk_size = 2**20
    for i in range(0, len(blob), chunk_size):
        chunk = blob[i : i + chunk_size]
        yield chunk


@app.get("/stream-item/{id}")
async def item_stream(id: str) -> StreamingResponse:
    return StreamingResponse(chunked_blob(db[id].blob), media_type="text/event-stream")


html = """
<!DOCTYPE html>
<html>
    <head>
        <title>WebSocket Echo</title>
    </head>
    <body>
        <h1>WebSocket Echo Test</h1>
        <form action="" onsubmit="sendMessage(event)">
            <input type="text" id="messageText" autocomplete="off"/>
            <button>Send</button>
        </form>
        <ul id="messages">
        </ul>
        <script>
            var ws = new WebSocket("ws://localhost:9080/websockets/ws");
            ws.onmessage = function(event) {
                var messages = document.getElementById('messages')
                var message = document.createElement('li')
                var content = document.createTextNode(event.data)
                message.appendChild(content)
                messages.appendChild(message)
            };
            function sendMessage(event) {
                var input = document.getElementById("messageText")
                ws.send(input.value)
                input.value = ''
                event.preventDefault()
            }
        </script>
    </body>
</html>
"""


@app.get("/websockets/")
async def get():
    return HTMLResponse(html)


@app.websocket("/websockets/ws")
async def websocket_endpoint(websocket: WebSocket):
    print("__PY__: here")
    await websocket.accept()
    print("__PY__: accepted")
    while True:
        print("__PY__: receiving text")
        data = await websocket.receive_text()
        print("__PY__: got", data)
        await websocket.send_text(data)
        print("__PY__: echoed message")
