const http = require("http")
const port = Number(process.env.PORT || 3000)
http
  .createServer((req, res) => {
    res.writeHead(200, { "content-type": "text/plain" })
    res.end("railpack node fixture\n")
  })
  .listen(port, "0.0.0.0")
