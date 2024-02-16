import uuid
from flask import Flask


app = Flask(__name__)

@app.route('/', defaults={'path': ''})
@app.route('/<path:path>')
def root(path):
    return f"Hello World {str(uuid.uuid4())}"
