from flask import Flask

app = Flask(__name__)


@app.get("/")
def index():
    return '<html><head><script src="/static/dist/app.js"></script></head><body>assets</body></html>'
