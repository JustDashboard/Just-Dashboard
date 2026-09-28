import { describe, expect, test } from "bun:test"
import { ROUTE_LIMIT, overviewRoutes, routeKind, routeTarget } from "./overview-routes"

const vhost = (overrides) => ({
  name: "app.example.com",
  kind: "nginx",
  path: "/etc/nginx/sites-available/app.example.com",
  enabledPath: "/etc/nginx/sites-enabled/app.example.com",
  enabled: true,
  serverNames: ["app.example.com"],
  listen: ["443 ssl"],
  upstreams: ["http://127.0.0.1:3000"],
  tls: true,
  modified: "2026-09-01T00:00:00Z",
  size: 100,
  ...overrides,
})

const caddyfile = vhost({
  name: "blog.example.com",
  kind: "caddy",
  path: "/etc/caddy/Caddyfile",
  enabledPath: undefined,
})
const ingress = vhost({
  name: "just-dashboard-shop",
  kind: "caddy",
  path: "",
  enabledPath: undefined,
  serverNames: ["shop.example.com"],
})

describe("the routes the overview draws", () => {
  // Ten sites named so the alphabet puts the plain one last: the overview
  // drew the first eight by name and never said there were two more.
  const hosts = [
    ...Array.from({ length: 8 }, (_, i) => vhost({ name: `site-${i}.example.com` })),
    vhost({ name: "zz-off.example.com", enabled: false }),
    vhost({ name: "zz-plain.example.com", tls: false, listen: ["80"] }),
  ]

  test("worst first, as many as fit, and how many there are", () => {
    const { shown, total } = overviewRoutes(hosts)
    expect(total).toBe(10)
    expect(shown).toHaveLength(ROUTE_LIMIT)
    expect(shown.map((v) => v.name).slice(0, 3)).toEqual([
      "zz-plain.example.com",
      "zz-off.example.com",
      "site-0.example.com",
    ])
  })

  test("the list it was given is left in its order", () => {
    overviewRoutes(hosts)
    expect(hosts[9].name).toBe("zz-plain.example.com")
  })

  test("a short list is shown whole", () => {
    expect(overviewRoutes([ingress, caddyfile])).toEqual({ shown: [caddyfile, ingress], total: 2 })
    expect(overviewRoutes([])).toEqual({ shown: [], total: 0 })
  })

  test("a Caddyfile site and a Docker ingress route are told apart", () => {
    expect(routeKind(vhost({}))).toBe("nginx site")
    expect(routeKind(caddyfile)).toBe("Caddyfile")
    expect(routeKind(ingress)).toBe("Docker Caddy ingress")
  })
})

describe("what pressing a route does", () => {
  // The site's page carries what each account may do there, so a reader is
  // never sent to the form, and a Docker ingress route with no file on the
  // host still has its requests and TLS report to read.
  test("every route opens the site's own page, for every reader", () => {
    expect(routeTarget(vhost({ name: "a b" }))).toEqual({
      href: "/proxy/sites/a%20b",
      verb: "Open a b",
    })
    expect(routeTarget(caddyfile)).toEqual({
      href: "/proxy/sites/blog.example.com",
      verb: "Open blog.example.com",
    })
    expect(routeTarget(ingress)).toEqual({
      href: "/proxy/sites/just-dashboard-shop",
      verb: "Open just-dashboard-shop",
    })
  })
})
