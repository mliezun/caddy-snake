from starlette.applications import Starlette
from starlette.responses import JSONResponse
from starlette.routing import Route


async def index(request):
    return JSONResponse(
        {
            "root_path": request.scope["root_path"],
            "path": request.scope["path"],
            "raw_path": request.scope["raw_path"].decode("latin-1"),
            "url_for_item": str(request.url_for("item", item_id=7)),
            "leaked": sorted(
                k.decode("latin-1") for k, _ in request.scope["headers"] if b"mount-prefix" in k
            ),
        }
    )


async def item(request):
    return JSONResponse(
        {
            "item_id": request.path_params["item_id"],
            "root_path": request.scope["root_path"],
            "path": request.scope["path"],
        }
    )


app = Starlette(
    routes=[
        Route("/", index),
        Route("/items/{item_id:int}", item, name="item"),
    ]
)
