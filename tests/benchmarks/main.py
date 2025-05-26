# main.py
# Pastebin-like API using FastAPI (works with Uvicorn/Hypercorn)
# Endpoints: POST /pastes, GET /pastes/{id}, DELETE /pastes/{id}

import os
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import asyncpg

DATABASE_URL = os.getenv(
    "DATABASE_URL", "postgresql://postgres:postgres@db:5432/postgres"
)

app = FastAPI()


class PasteIn(BaseModel):
    content: str


class PasteOut(BaseModel):
    id: int
    content: str


@app.on_event("startup")
async def startup():
    app.state.pool = await asyncpg.create_pool(DATABASE_URL)
    async with app.state.pool.acquire() as conn:
        await conn.execute("""
        CREATE TABLE IF NOT EXISTS pastes (
            id SERIAL PRIMARY KEY,
            content TEXT NOT NULL
        )
        """)


@app.on_event("shutdown")
async def shutdown():
    await app.state.pool.close()


@app.post("/pastes", response_model=PasteOut)
async def create_paste(paste: PasteIn):
    async with app.state.pool.acquire() as conn:
        row = await conn.fetchrow(
            "INSERT INTO pastes (content) VALUES ($1) RETURNING id, content",
            paste.content,
        )
        return PasteOut(id=row["id"], content=row["content"])


@app.get("/pastes/{paste_id}", response_model=PasteOut)
async def get_paste(paste_id: int):
    async with app.state.pool.acquire() as conn:
        row = await conn.fetchrow(
            "SELECT id, content FROM pastes WHERE id=$1", paste_id
        )
        if not row:
            raise HTTPException(status_code=404, detail="Paste not found")
        return PasteOut(id=row["id"], content=row["content"])


@app.delete("/pastes/{paste_id}")
async def delete_paste(paste_id: int):
    async with app.state.pool.acquire() as conn:
        result = await conn.execute("DELETE FROM pastes WHERE id=$1", paste_id)
        if result == "DELETE 0":
            raise HTTPException(status_code=404, detail="Paste not found")
        return {"status": "deleted"}
