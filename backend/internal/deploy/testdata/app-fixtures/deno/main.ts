const port = Number(Deno.env.get("PORT") ?? "8000")
Deno.serve({ port, hostname: "0.0.0.0" }, () =>
  new Response("<h1>https://deno.build-value.test</h1>", {
    headers: { "content-type": "text/html" },
  }),
)
