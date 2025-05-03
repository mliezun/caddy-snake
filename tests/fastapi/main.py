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
    print("Get item", file=sys.stderr)
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


@app.post("/item/upload-file/")
async def upload_file(file: bytes):
    # Return the content of the uploaded file
    return file
