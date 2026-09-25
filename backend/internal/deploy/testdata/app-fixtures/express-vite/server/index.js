import express from "express"
import path from "path"

const app = express()
app.get("/api/health", (_request, response) => response.json({ ok: true }))
app.use(express.static(path.resolve(import.meta.dirname, "public")))
const port = parseInt(process.env.PORT || "5000", 10)
app.listen(port, "0.0.0.0", () => console.log(`serving on port ${port}`))
