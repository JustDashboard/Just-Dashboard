import { describe, expect, test } from "bun:test"
import {
  certRunway,
  errorTone,
  listenerAt,
  placeOnRunway,
  runwayTone,
  siteApp,
  siteHue,
  sitesVerdict,
  trafficShares,
  upstreamEndpoint,
} from "./site-apps"

const vhost = (overrides) => ({
  name: "app.example.com",
  kind: "nginx",
  path: "/etc/nginx/sites-available/app.example.com",
  enabledPath: "/etc/nginx/sites-enabled/app.example.com",
  enabled: true,
  formEditable: true,
  serverNames: ["app.example.com"],
  listen: ["443 ssl"],
  upstreams: ["http://127.0.0.1:3000"],
  tls: true,
  modified: "2026-09-01T00:00:00Z",
  size: 100,
  ...overrides,
})

const listener = (overrides) => ({
  protocol: "tcp",
  family: "ipv4",
  address: "127.0.0.1",
  port: 3000,
  pid: 1400,
  process: "node",
  scope: "loopback",
  reach: "loopback",
  network: "loopback",
  exposed: false,
  ...overrides,
})

const cert = (overrides) => ({
  name: "app.example.com",
  path: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
  domains: ["app.example.com"],
  issuer: "R11",
  notBefore: "2026-09-01T00:00:00Z",
  notAfter: "2026-11-30T00:00:00Z",
  daysLeft: 50,
  expired: false,
  expiring: false,
  selfSigned: false,
  source: "certbot",
  usedBy: ["app.example.com"],
  ...overrides,
})

describe("upstreamEndpoint", () => {
  test("fills the port from the scheme", () => {
    expect(upstreamEndpoint("http://127.0.0.1")).toEqual({ host: "127.0.0.1", port: 80 })
    expect(upstreamEndpoint("https://api.internal/v1")).toEqual({ host: "api.internal", port: 443 })
    expect(upstreamEndpoint("http://[::1]:8080/")).toEqual({ host: "[::1]", port: 8080 })
  })

  test("a unix socket has no endpoint", () => {
    expect(upstreamEndpoint("http://unix:/run/app.sock")).toBeUndefined()
  })
})

describe("listenerAt", () => {
  test("finds the socket loopback reaches, the container's first", () => {
    const proxy = listener({ address: "0.0.0.0", process: "docker-proxy" })
    const container = listener({
      address: "0.0.0.0",
      process: "docker-proxy",
      container: { id: "c1", name: "grafana", image: "grafana/grafana:11" },
    })
    expect(listenerAt("http://127.0.0.1:3000", [proxy, container])).toBe(container)
  })

  test("never reads a remote upstream or a socket bound elsewhere", () => {
    expect(listenerAt("http://10.0.0.5:3000", [listener({})])).toBeUndefined()
    expect(listenerAt("http://127.0.0.1:3000", [listener({ address: "10.0.0.2" })])).toBeUndefined()
  })
})

describe("siteApp", () => {
  test("names a container by its image and a program by its name", () => {
    const grafana = listener({
      process: "docker-proxy",
      container: { id: "c1", name: "grafana", image: "grafana/grafana:11.2.0" },
    })
    expect(siteApp(vhost({}), [grafana], [])).toEqual({
      address: "127.0.0.1:3000",
      product: "grafana",
      name: "grafana",
    })
    expect(siteApp(vhost({}), [listener({})], [])).toEqual({
      address: "127.0.0.1:3000",
      product: "nodejs",
      name: "node",
    })
  })

  test("draws no logo it cannot read off the socket", () => {
    // An image that names nothing is Docker's, which says nothing of the app.
    const bare = listener({ container: { id: "c1", name: "shop", image: "c9051a2ac152" } })
    expect(siteApp(vhost({}), [bare], []).product).toBeUndefined()
    // No socket on the list: the port alone is not a product.
    expect(siteApp(vhost({ upstreams: ["http://127.0.0.1:5432"] }), [], [])).toEqual({
      address: "127.0.0.1:5432",
      product: undefined,
      name: undefined,
    })
  })

  test("follows an upstream block to its first server", () => {
    const site = vhost({
      upstreams: ["http://app_pool"],
      pools: [{ name: "app_pool", servers: ["127.0.0.1:3000", "127.0.0.1:3001"] }],
    })
    expect(siteApp(site, [listener({})], []).address).toBe("127.0.0.1:3000")
  })

  test("falls back to the health check's owner, and proxies nothing for a static site", () => {
    const health = [{ address: "127.0.0.1:3000", owner: "grafana-server", state: "up" }]
    expect(siteApp(vhost({}), [], health).product).toBe("grafana")
    expect(siteApp(vhost({ upstreams: [] }), [], [])).toBeUndefined()
  })
})

test("a site's hue is stable and never a state's", () => {
  expect(siteHue("App.example.com")).toBe(siteHue("app.example.com"))
  for (const name of ["a", "b", "c", "grafana", "git", "n8n", "cloud", "home"]) {
    expect(["var(--tag-red)", "var(--tag-amber)", "var(--tag-slate)"]).not.toContain(siteHue(name))
  }
})

describe("trafficShares", () => {
  const traffic = {
    observedAt: "2026-10-10T00:00:00Z",
    sites: [
      {
        site: "a",
        file: "",
        status: "available",
        requests: 10,
        errorRate: 0,
        bytes: 0,
        complete: true,
      },
      {
        site: "b",
        file: "",
        status: "available",
        requests: 90,
        errorRate: 0,
        bytes: 0,
        complete: true,
      },
      {
        site: "c",
        file: "",
        status: "available",
        requests: 0,
        errorRate: 0,
        bytes: 0,
        complete: true,
      },
      {
        site: "d",
        file: "",
        status: "unavailable",
        requests: 0,
        errorRate: 0,
        bytes: 0,
        complete: true,
      },
    ],
  }

  test("busiest first, quiet and unread counted apart", () => {
    const shares = trafficShares(
      ["a", "b", "c", "d"].map((name) => vhost({ name })),
      traffic,
    )
    expect(shares.busy.map((s) => s.vhost.name)).toEqual(["b", "a"])
    expect(shares.total).toBe(100)
    expect(shares.quiet).toBe(1)
    expect(shares.unread).toBe(1)
  })

  test("no summary is no reading, not a quiet hour", () => {
    expect(trafficShares([vhost({})], undefined)).toBeUndefined()
  })
})

test("errorTone", () => {
  expect(errorTone(0.004)).toBeUndefined()
  expect(errorTone(0.02)).toBe("warning")
  expect(errorTone(0.18)).toBe("danger")
})

describe("certRunway", () => {
  test("soonest first, with plain HTTP and unread certificates counted", () => {
    const sites = [
      vhost({ name: "late.example.com" }),
      vhost({ name: "soon.example.com" }),
      vhost({ name: "caddy-route", kind: "caddy", path: "" }),
      vhost({ name: "plain.example.com", tls: false }),
      vhost({ name: "off.example.com", enabled: false }),
    ]
    const certs = [
      cert({ name: "late", path: "/l", usedBy: ["late.example.com"], daysLeft: 80 }),
      cert({ name: "soon", path: "/s", usedBy: ["soon.example.com"], daysLeft: 6 }),
      cert({ name: "off", path: "/o", usedBy: ["off.example.com"], daysLeft: 1 }),
    ]
    const runway = certRunway(sites, certs)
    expect(runway.rows.map((r) => r.vhost.name)).toEqual(["soon.example.com", "late.example.com"])
    expect(runway.plain.map((v) => v.name)).toEqual(["plain.example.com"])
    expect(runway.unknown).toBe(1)
    expect(runway.tls).toBe(3)
  })

  test("the axis and its tones", () => {
    expect(placeOnRunway(-3)).toBe(0)
    expect(placeOnRunway(45)).toBe(50)
    expect(placeOnRunway(400)).toBe(100)
    expect(runwayTone(cert({ daysLeft: 50 }))).toBeUndefined()
    expect(runwayTone(cert({ daysLeft: 9 }))).toBe("warning")
    expect(runwayTone(cert({ daysLeft: -1, expired: true }))).toBe("danger")
  })
})

describe("sitesVerdict", () => {
  const base = { isDown: () => false, stopped: false, engine: "nginx", total: 3 }

  test("says each site once, by the worst thing about it", () => {
    const plain = vhost({ name: "plain", tls: false })
    const down = vhost({ name: "down" })
    const verdict = sitesVerdict({
      ...base,
      attention: [plain, down],
      isDown: (v) => v.name === "down",
    })
    expect(verdict).toEqual({
      tone: "danger",
      label: "2 sites need attention",
      detail: "1 application refusing · 1 on plain HTTP",
      chip: "attention",
    })
  })

  test("a lone warning is amber, and nothing to act on is said plainly", () => {
    const parked = vhost({ name: "parked", enabled: false })
    expect(sitesVerdict({ ...base, attention: [parked] })).toMatchObject({
      tone: "warning",
      label: "1 site needs attention",
      detail: "1 disabled",
    })
    expect(sitesVerdict({ ...base, attention: [] })).toEqual({
      tone: "running",
      label: "Nothing needs attention",
    })
    expect(sitesVerdict({ ...base, attention: [], stopped: true })).toEqual({
      tone: "stopped",
      label: "No nginx site is served",
    })
  })
})
