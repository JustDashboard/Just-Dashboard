from litestar import Litestar, MediaType, get


@get("/", media_type=MediaType.HTML)
async def index() -> str:
    return "<h1>https://python-pdm.build-value.test</h1>"


app = Litestar([index])
