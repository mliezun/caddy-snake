import wsgiref.validate
from flask import Flask, request


app = Flask(__name__)

db = {}


@app.route("/item/<string:id>", methods=["POST"])
def store_item(id: str):
    content = request.get_json()
    db[id] = content
    return "Stored"


@app.route("/item/<string:id>", methods=["GET"])
def get_item(id: str):
    return db.get(id)


@app.route("/item/<string:id>", methods=["DELETE"])
def delete_item(id):
    del db[id]
    return "Deleted"


app = wsgiref.validate.validator(app)
