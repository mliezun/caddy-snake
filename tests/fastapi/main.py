from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()

db = {}

class Item(BaseModel):
    name: str
    description: str

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
