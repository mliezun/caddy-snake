from fastapi import FastAPI, Request

app = FastAPI()

@app.get("/{full_path:path}")
async def root(full_path: str, request: Request):
    print("Inside FastAPI!!!")
    return {"message": "Hello World", "path": full_path}
