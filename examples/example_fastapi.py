from fastapi import FastAPI, Request
from a2wsgi import ASGIMiddleware

app = FastAPI()

@app.get("/{full_path:path}")
async def root(full_path: str, request: Request):
    return {"message": "Hello World", "path": full_path}


app = ASGIMiddleware(app)
