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
  test("an administrator opens the site on the Sites page", () => {
    expect(routeTarget(vhost({ name: "a b" }), true)).toEqual({
      open: "page",
      href: "/proxy/sites?site=a%20b",
      verb: "Open a b",
    })
    expect(routeTarget(caddyfile, true)).toEqual({
      open: "page",
      href: "/proxy/sites?site=blog.example.com",
      verb: "Open blog.example.com",
    })
  })

  // The Sites link opened the site form, which a reader may not preview or save.
  test("anyone else reads the file, never the form", () => {
    expect(routeTarget(vhost({}), false)).toEqual({
      open: "file",
      verb: "View app.example.com",
    })
    expect(routeTarget(caddyfile, false)).toEqual({ open: "file", verb: "View blog.example.com" })
  })

  // A Docker ingress route has no file on the host, and the link to the
  // Sites page opened nothing.
  test("an ingress route opens its live TLS report for an administrator", () => {
    expect(routeTarget(ingress, true)).toEqual({
      open: "page",
      href: "/proxy/tls?domain=shop.example.com",
      verb: "TLS report for shop.example.com",
    })
  })

  test("an ingress route has nothing to open for a reader, or without a domain on TLS", () => {
    expect(routeTarget(ingress, false)).toEqual({ open: "none", verb: "just-dashboard-shop" })
    expect(routeTarget({ ...ingress, tls: false }, true).open).toBe("none")
    expect(routeTarget({ ...ingress, serverNames: ["*.example.com"] }, true).open).toBe("none")
    expect(routeTarget({ ...ingress, serverNames: [] }, true).open).toBe("none")
  })
})
