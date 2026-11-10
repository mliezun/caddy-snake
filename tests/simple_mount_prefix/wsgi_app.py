from flask import Flask, jsonify, redirect, request, url_for

app = Flask(__name__)


@app.get("/")
def index():
    return jsonify(
        script_name=request.environ["SCRIPT_NAME"],
        path_info=request.environ["PATH_INFO"],
        url_for_item=url_for("item", item_id=7),
        leaked=sorted(k for k in request.environ if "MOUNT_PREFIX" in k),
    )


@app.get("/items/<int:item_id>")
def item(item_id):
    return jsonify(
        item_id=item_id,
        script_name=request.environ["SCRIPT_NAME"],
        path_info=request.environ["PATH_INFO"],
        query=request.args.get("q"),
    )


@app.get("/go")
def go():
    return redirect(url_for("item", item_id=1))
