from fastapi import FastAPI, Request

app = FastAPI()


@app.get("/{full_path:path}")
async def root(full_path: str, request: Request):
    return {"message": "Hello World", "path": full_path}
