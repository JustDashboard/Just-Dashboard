import { describe, expect, test } from "bun:test"
import {
  beamDuration,
  controlState,
  probeCount,
  siteFeatures,
  siteVerdict,
  upstreamAddress,
  upstreamOwner,
  upstreamUnheard,
  visitorShares,
} from "./site-overview"

const hour = (fields = {}) => ({
  total: 1000,
  classes: { "2xx": 990, "5xx": 0 },
  errorRate: 0,
  perMinute: 16.7,
  agents: [],
  probes: [],
  ...fields,
})

const node = {
  protocol: "tcp",
  family: "ipv4",
  address: "127.0.0.1",
  port: 3000,
  pid: 2210,
  process: "MainThread",
  displayName: "node",
}

describe("site verdict", () => {
  test("failed requests come first, and narrow Requests to them", () => {
    const verdict = siteVerdict({
      vhost: { enabled: true },
      summary: hour({ classes: { "5xx": 8 }, errorRate: 0.008 }),
      pools: [{ verdict: "down", members: [] }],
    })
    expect(verdict).toEqual({
      tone: "warning",
      label: "8 failed in the last hour",
      hint: "0.8% of 1,000 answered 5xx",
      narrows: "5xx",
    })
    expect(
      siteVerdict({
        vhost: { enabled: true },
        summary: hour({ classes: { "5xx": 40 }, errorRate: 0.04 }),
        pools: [],
      }).tone,
    ).toBe("danger")
  })

  test("then servers that will not answer, then the certificate", () => {
    const failing = {
      verdict: "degraded",
      members: [{ state: "up" }, { state: "refused" }, { state: "up" }],
    }
    expect(siteVerdict({ vhost: { enabled: true }, summary: hour(), pools: [failing] })).toEqual({
      tone: "warning",
      label: "1 of 3 servers failing",
    })
    expect(
      siteVerdict({ vhost: { enabled: true }, pools: [{ verdict: "down", members: [] }] }).tone,
    ).toBe("danger")
    expect(
      siteVerdict({
        vhost: { enabled: true },
        summary: hour(),
        pools: [],
        cert: { expiring: true, daysLeft: 12 },
      }).label,
    ).toBe("Its certificate ends in 12 days")
  })

  test("a well site says how much it answered; a disabled one has no verdict", () => {
    expect(siteVerdict({ vhost: { enabled: true }, summary: hour(), pools: [] })).toEqual({
      tone: "running",
      label: "Every request answered",
      hint: "1,000 in the last hour",
    })
    expect(
      siteVerdict({ vhost: { enabled: true }, summary: hour({ total: 0 }), pools: [] }).label,
    ).toBe("No requests in the last hour")
    expect(siteVerdict({ vhost: { enabled: false }, summary: hour(), pools: [] })).toBeUndefined()
    expect(siteVerdict({ vhost: { enabled: true }, pools: [] })).toBeUndefined()
  })
})

describe("site features", () => {
  test("the form's switches, then what the server found in the file, each once", () => {
    const words = siteFeatures(
      { tls: true, features: ["h2", "ratelimit"] },
      { tls: true, forceHttps: true, hsts: true, http2: true, webSockets: false, gzip: true },
    ).map((word) => word.label)
    expect(words).toEqual(["HTTPS only", "HSTS", "HTTP/2", "gzip", "rate-limited"])
  })

  test("HTTPS words need TLS, and a kind keeps its hue", () => {
    const words = siteFeatures({ tls: false }, { tls: false, forceHttps: true, hsts: true })
    expect(words).toEqual([])
    const [guard, protocol] = siteFeatures({ tls: true, features: ["auth", "ws"] }, undefined)
    expect(guard).toEqual({ label: "password", hue: "var(--tag-green)" })
    expect(protocol.hue).toBe("var(--tag-blue)")
  })
})

describe("upstreams", () => {
  test("an address reads with or without a scheme", () => {
    expect(upstreamAddress("http://127.0.0.1:3000")).toEqual({ host: "127.0.0.1", port: 3000 })
    expect(upstreamAddress("10.0.0.2:8080")).toEqual({ host: "10.0.0.2", port: 8080 })
    expect(upstreamAddress("https://api.internal")).toEqual({ host: "api.internal", port: 443 })
    expect(upstreamAddress("http://[::1]:9000")).toEqual({ host: "::1", port: 9000 })
    expect(upstreamAddress("unix:/run/php/php8.3-fpm.sock")).toBeUndefined()
  })

  test("what holds the socket answers a loopback upstream, drawn as its program", () => {
    expect(upstreamOwner("http://127.0.0.1:3000", "app", [node])).toEqual({
      product: "nodejs",
      name: "node",
      pid: 2210,
    })
    expect(
      upstreamOwner("http://127.0.0.1:3000", "app", [
        { ...node, pid: 0, container: { id: "abc", name: "shop-web", image: "redis:7" } },
      ]),
    ).toEqual({ product: "redis", name: "shop-web", pid: undefined })
  })

  test("another machine's address is not this host's socket on the same port", () => {
    const routed = { ...node, routes: [{ site: "app", tls: true }] }
    expect(upstreamOwner("10.0.0.3:3000", "app", [routed])).toBeUndefined()
    expect(upstreamOwner("http://127.0.0.1:3000", "app", undefined)).toBeUndefined()
  })

  test("nothing listening is said only for this machine, once the sockets were read", () => {
    expect(upstreamUnheard("http://127.0.0.1:3001", [node])).toBe(true)
    expect(upstreamUnheard("http://127.0.0.1:3000", [node])).toBe(false)
    expect(upstreamUnheard("http://10.0.0.3:3001", [node])).toBe(false)
    expect(upstreamUnheard("http://127.0.0.1:3001", undefined)).toBe(false)
  })
})

describe("visitors", () => {
  test("browsers and bots are shared out of every request, a family without a mark left out", () => {
    const summary = hour({
      total: 200,
      agents: [
        { value: "Safari", count: 50 },
        { value: "Chrome", count: 120 },
        { value: "Mozilla", count: 30 },
      ],
    })
    expect(visitorShares(summary)).toEqual([
      { product: "chrome", share: 0.6 },
      { product: "safari", share: 0.25 },
    ])
    expect(visitorShares(hour({ total: 0 }))).toEqual([])
  })

  test("probes are counted, and a busier site's pulse runs quicker", () => {
    expect(probeCount(hour({ probes: [{ count: 12 }, { count: 9 }] }))).toBe(21)
    expect(beamDuration(0)).toBe(3)
    expect(beamDuration(1000)).toBeLessThan(beamDuration(10))
    expect(beamDuration(1e9)).toBe(1.2)
  })
})

describe("controls", () => {
  test("a control is on, off, or set on a build that cannot honour it", () => {
    expect(controlState({ configured: true, support: "built-in" })).toEqual({
      tone: "running",
      label: "on",
    })
    expect(controlState({ configured: false, support: "built-in" }).label).toBe("off")
    expect(controlState({ configured: true, support: "missing" }).tone).toBe("warning")
  })
})
