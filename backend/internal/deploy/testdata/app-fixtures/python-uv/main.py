import sys

from fastapi import FastAPI
from fastapi.responses import HTMLResponse

app = FastAPI()


@app.get("/", response_class=HTMLResponse)
def index() -> str:
    return f"<h1>https://python-uv.build-value.test</h1><p>{sys.version_info.major}.{sys.version_info.minor}</p>"
