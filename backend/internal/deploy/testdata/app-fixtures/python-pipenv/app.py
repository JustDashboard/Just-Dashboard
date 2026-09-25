from flask import Flask

app = Flask(__name__)


@app.get("/")
def index():
    return "<h1>https://python-pipenv.build-value.test</h1>"
