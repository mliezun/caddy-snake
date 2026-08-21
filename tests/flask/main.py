import wsgiref.validate

from flask import Flask, request
from kvstore import KVStore

app = Flask(__name__)

db = KVStore("items.db")


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


@app.route("/item/upload-file/", methods=["POST"])
def upload():
    uploaded_file = request.files.get("file")
    if uploaded_file and uploaded_file.filename:
        # Return the content of the uploaded file
        return uploaded_file.read()
    return "No file uploaded", 400


@app.route("/encoding/param/<name>")
def encoding_param(name):
    # Werkzeug decodes PATH_INFO latin-1 octets as UTF-8 (issue #237).
    return f"{name}, {[hex(ord(c)) for c in name]}"


@app.route("/encoding/path_info/<name>")
def encoding_path_info(name):
    raw = request.environ.get("PATH_INFO", "").rsplit("/", 1)[-1]
    return f"{raw}, {[hex(ord(c)) for c in raw]}"


app = wsgiref.validate.validator(app)
