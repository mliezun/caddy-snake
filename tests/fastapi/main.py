import sys
from typing import Optional
from contextlib import asynccontextmanager

from fastapi import FastAPI
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
