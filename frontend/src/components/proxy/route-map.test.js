import { describe, expect, test } from "bun:test"
import { backendOf, requestPulse, routeMap, routeVerdict, siteBackend } from "./route-map"

const listener = (port, extra = {}) => ({
  protocol: "tcp",
  family: "ipv4",
  address: "127.0.0.1",
  port,
  pid: 100 + port,
  process: "node",
  scope: "loopback",
  reach: "loopback",
  network: "loopback",
  exposed: false,
  ...extra,
})

const container = (port, name, image) =>
  listener(port, {
    process: "docker-proxy",
    manager: "container",
    container: { id: `id-${name}`, name, image, published: true },
  })

const site = (name, extra = {}) => ({
  name,
  kind: "nginx",
  path: `/etc/nginx/sites-available/${name}`,
  enabledPath: `/etc/nginx/sites-enabled/${name}`,
  enabled: true,
  formEditable: true,
  serverNames: [name],
  listen: ["443 ssl"],
  upstreams: [],
  tls: true,
  certPath: `/etc/letsencrypt/live/${name}/fullchain.pem`,
  modified: "",
  size: 0,
  ...extra,
})

const target = (vhost, address, state, extra = {}) => ({
  site: vhost.name,
  kind: "http",
  directive: "proxy_pass",
  address,
  file: vhost.path,
  line: 1,
  state,
  ...extra,
})

const hour = (name, requests, errorRate = 0) => ({
  site: name,
  file: "",
  status: "available",
  requests,
  errorRate,
  bytes: 0,
  complete: true,
})

describe("what answers behind a route", () => {
  test("a loopback port is the container or the program listening on it", () => {
    const ports = [container(3001, "grafana", "grafana/grafana:11"), listener(3000)]
    expect(backendOf("http://127.0.0.1:3001", ports)).toEqual({
      product: "grafana",
      name: "grafana",
      address: "127.0.0.1:3001",
      container: "id-grafana",
    })
    expect(backendOf("http://localhost:3000/", ports)).toMatchObject({
      product: "nodejs",
      name: "node",
      address: "localhost:3000",
    })
  })

  test("without a port list, the owner the upstream check found names it", () => {
    expect(backendOf("http://127.0.0.1:8080", [], "python3")).toMatchObject({
      product: "python",
      name: "python3",
    })
  })

  test("a name on Docker's network is its container, else the product it names", () => {
    const ports = [container(5678, "n8n", "n8nio/n8n:1.62")]
    expect(backendOf("http://n8n:5678", ports)).toMatchObject({ product: "n8n", name: "n8n" })
    expect(backendOf("http://grafana:3000", [])).toMatchObject({ product: "grafana" })
  })

  test("a socket is the program its file is named for", () => {
    expect(backendOf("unix:/run/php/php8.3-fpm.sock", [])).toMatchObject({
      product: "php",
      address: "/run/php/php8.3-fpm.sock",
    })
  })

  // A guessed logo on an address nothing answers for would be the drawing
  // lying about the route.
  test("an address nothing names keeps no product", () => {
    expect(backendOf("http://127.0.0.1:8000", [])).toEqual({
      name: "127.0.0.1:8000",
      address: "127.0.0.1:8000",
    })
    expect(backendOf("http://10.0.0.7:9000", [])).toEqual({
      name: "10.0.0.7:9000",
      address: "10.0.0.7:9000",
    })
  })

  test("a site with no upstream serves files, redirects or its own configuration", () => {
    expect(siteBackend(site("docs", { roots: ["/var/www/docs"] }), [])).toMatchObject({
      kind: "files",
      address: "/var/www/docs",
    })
    expect(siteBackend(site("old", { redirects: ["https://new.example.com"] }), [])).toMatchObject({
      kind: "redirect",
    })
    expect(siteBackend(site("plain"), []).kind).toBe("config")
  })
})

describe("the route map", () => {
  const app = site("app.example.com", { upstreams: ["http://127.0.0.1:3000"] })
  const www = site("www.example.com", { upstreams: ["http://127.0.0.1:3000"] })
  const api = site("api.example.com", { upstreams: ["http://127.0.0.1:8000"] })
  const off = site("off.example.com", { enabled: false, upstreams: ["http://127.0.0.1:9"] })
  const upstreams = {
    checkedAt: "",
    targets: [
      target(app, "127.0.0.1:3000", "up", { ms: 2 }),
      target(www, "127.0.0.1:3000", "up", { ms: 3 }),
      target(api, "127.0.0.1:8000", "refused"),
    ],
  }

  test("leads with a route that is down, then the busiest, and draws no disabled site", () => {
    const map = routeMap({
      vhosts: [app, www, api, off],
      listeners: [listener(3000)],
      upstreams,
      traffic: [
        hour("app.example.com", 900),
        hour("www.example.com", 4000),
        hour("api.example.com", 10),
      ],
    })
    expect(map.domains.map((d) => d.id)).toEqual([
      "api.example.com",
      "www.example.com",
      "app.example.com",
    ])
    expect(map.domains[0].down).toBe(true)
    expect(map.total).toBe(3)
    expect(map.hidden).toBe(0)
  })

  test("two domains sending to one application meet at one node, their hours summed", () => {
    const map = routeMap({
      vhosts: [app, www],
      listeners: [listener(3000)],
      upstreams,
      traffic: [hour("app.example.com", 900), hour("www.example.com", 4000)],
    })
    expect(map.apps).toHaveLength(1)
    expect(map.apps[0]).toMatchObject({
      product: "nodejs",
      state: "up",
      requests: 4900,
      domains: ["www.example.com", "app.example.com"],
    })
  })

  test("past its limit it counts the routes it left out", () => {
    const many = Array.from({ length: 11 }, (_, i) => site(`s${i}.example.com`))
    const map = routeMap({ vhosts: many, listeners: [] })
    expect(map.domains).toHaveLength(8)
    expect(map.hidden).toBe(3)
  })
})

describe("the verdict on the routes", () => {
  const a = site("a", { upstreams: ["http://127.0.0.1:1"] })
  const b = site("b", { upstreams: ["http://127.0.0.1:2", "http://127.0.0.1:3"] })

  test("says nothing until the upstreams were checked", () => {
    expect(routeVerdict([a, b], undefined)).toBeUndefined()
  })

  test("counts the routes down, then those partly down, then says all answer", () => {
    const report = (states) => ({
      checkedAt: "",
      targets: [
        target(a, "127.0.0.1:1", states[0]),
        target(b, "127.0.0.1:2", states[1]),
        target(b, "127.0.0.1:3", states[2]),
      ],
    })
    expect(routeVerdict([a, b], report(["refused", "up", "up"]))).toEqual({
      tone: "critical",
      label: "1 route down",
    })
    expect(routeVerdict([a, b], report(["up", "timeout", "up"]))).toEqual({
      tone: "warning",
      label: "1 route partly down",
    })
    expect(routeVerdict([a, b], report(["up", "up", "up"]))).toEqual({
      tone: "ok",
      label: "All 2 routes answering",
    })
  })
})

describe("how fast a pulse runs", () => {
  test("slowest at a request a minute, quicker the busier, never faster than 1.5s", () => {
    expect(requestPulse(10)).toBe(4)
    expect(requestPulse(6_000)).toBeLessThan(requestPulse(600))
    expect(requestPulse(10_000_000)).toBe(1.5)
  })
})
