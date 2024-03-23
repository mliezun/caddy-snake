from fastapi import FastAPI
from a2wsgi import ASGIMiddleware

app = FastAPI()

@app.get("/app4")
async def root():
    return {"message": "Hello World"}


app = ASGIMiddleware(app)
