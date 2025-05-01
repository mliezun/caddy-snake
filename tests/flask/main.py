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

@app.route('/item/upload-file/', methods=['POST'])
def upload():
    uploaded_file = request.files.get('file')
    if uploaded_file and uploaded_file.filename:
        # Return the content of the uploaded file
        return uploaded_file.read()
    return "No file uploaded", 400

app = wsgiref.validate.validator(app)
