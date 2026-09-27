import { expect, test } from "bun:test"
import { NGINX_ERROR_LOG, siteHosts, siteLogPlan } from "./site-log-plan"

const vhost = (over) => ({
  name: "shop",
  kind: "nginx",
  path: "/etc/nginx/sites-available/shop",
  enabled: true,
  serverNames: ["shop.example.com", "www.shop.example.com"],
  listen: ["443 ssl"],
  upstreams: ["http://127.0.0.1:3000"],
  tls: true,
  modified: "",
  size: 0,
  ...over,
})

const spec = (over) => ({ name: "shop", accessLog: true, ...over })

test("an nginx site with its own files reads them, and its requests beside the access log", () => {
  const plan = siteLogPlan(
    vhost(),
    spec({
      accessLogPath: "/var/log/nginx/shop.access.log",
      errorLogPath: "/var/log/nginx/shop.error.log",
    }),
    undefined,
  )
  expect(plan.sources.map((s) => [s.id, s.lens])).toEqual([
    ["file:/var/log/nginx/shop.access.log", "http-access"],
    ["file:/var/log/nginx/shop.error.log", "nginx-error"],
  ])
  expect(plan.requests).toBe("file:/var/log/nginx/shop.access.log")
  // Its own error log is its own: nothing to narrow.
  expect(plan.errors).toEqual({
    source: "file:/var/log/nginx/shop.error.log",
    lens: "nginx-error",
    fields: {},
    shared: false,
  })
  expect(plan.unrecorded).toBeUndefined()
})

test("a hand-written site with no files of its own reads nginx's, narrowed to its names", () => {
  const plan = siteLogPlan(vhost(), spec({}), undefined)
  expect(plan.sources.map((s) => s.id)).toEqual([`file:${NGINX_ERROR_LOG}`])
  expect(plan.requests).toBeUndefined()
  expect(plan.errors.fields).toEqual({ host: ["shop.example.com", "www.shop.example.com"] })
  expect(plan.errors.shared).toBe(true)
  expect(plan.unrecorded).toBe("shared")
  // A catch-all has no name to narrow by, and its log is still not its own.
  const catchAll = siteLogPlan(vhost({ serverNames: ["_"] }), spec({}), undefined)
  expect(catchAll.errors).toMatchObject({ fields: {}, shared: true })
  // A site that turned its log off is told apart from one sharing nginx's.
  expect(siteLogPlan(vhost(), spec({ accessLog: false }), undefined).unrecorded).toBe("off")
})

test("a route on the Docker Caddy ingress reads its record and the ingress's output", () => {
  const route = vhost({ name: "just-dashboard-env-7", kind: "caddy", path: "" })
  const plan = siteLogPlan(route, undefined, "edge")
  expect(plan.sources.map((s) => [s.id, s.lens])).toEqual([["docker:edge", "caddy"]])
  expect(plan.requests).toBe("docker:edge")
  expect(plan.errors.fields.host).toEqual(["shop.example.com", "www.shop.example.com"])
  expect(plan.errors.shared).toBe(true)
  // Caddy's certificate lines name the domain, not a host: narrowed by that.
  expect(plan.errors.certificates).toEqual({
    domain: ["shop.example.com", "www.shop.example.com"],
  })
  // Without the ingress there is nothing to read it through.
  expect(siteLogPlan(route, undefined, undefined).sources).toEqual([])
})

test("an unmanaged host on an ingress and a Caddyfile site read Caddy's own output only", () => {
  const listed = siteLogPlan(
    vhost({ name: "docker-caddy:edge:blog.example.com", kind: "caddy", path: "" }),
    undefined,
    "edge",
  )
  expect(listed.sources.map((s) => s.id)).toEqual(["docker:edge"])
  expect(listed.requests).toBeUndefined()

  const file = siteLogPlan(
    vhost({ name: "Caddyfile", kind: "caddy", path: "/etc/caddy/Caddyfile" }),
    undefined,
    undefined,
  )
  expect(file.sources.map((s) => [s.id, s.lens])).toEqual([["journal:caddy.service", "caddy"]])
  expect(file.requests).toBeUndefined()
  // The Caddyfile's names are addresses — `:80`, `https://x` — that no line
  // carries as its host, and its journal is all of it: nothing to narrow.
  const addressed = siteLogPlan(
    vhost({
      name: "Caddyfile",
      kind: "caddy",
      path: "/etc/caddy/Caddyfile",
      serverNames: [":80", "https://blog.example.com", "shop.example.com:443"],
    }),
    undefined,
    undefined,
  )
  expect(addressed.errors).toEqual({
    source: "journal:caddy.service",
    lens: "caddy",
    fields: {},
    shared: false,
  })
})

test("a wildcard, a regex name and nginx's catch-all equal no host a line can carry", () => {
  expect(
    siteHosts({ serverNames: ["_", "*.example.com", "~^www\\d+", "app.example.com"] }),
  ).toEqual(["app.example.com"])
})
