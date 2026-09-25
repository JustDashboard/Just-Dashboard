import { Hono } from "hono"

const app = new Hono()
app.get("/", (c) => c.html(`<h1>${process.env.API_URL}</h1>`))

export default app
