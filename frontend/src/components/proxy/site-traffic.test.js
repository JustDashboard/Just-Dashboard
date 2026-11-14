import { describe, expect, test } from "bun:test"
import {
  busiestSites,
  edgeHour,
  failingWords,
  legacySiteTarget,
  readingHref,
  trafficTab,
} from "./site-traffic"

const at = (minute) => `2026-11-13T10:${String(minute).padStart(2, "0")}:00Z`

function site(name, over = {}) {
  return {
    site: name,
    file: `/etc/nginx/sites-available/${name}`,
    engine: "nginx",
    status: "available",
    requests: 0,
    errorRate: 0,
    bytes: 0,
    complete: true,
    points: [],
    ...over,
  }
}

describe("the edge's hour", () => {
  test("adds up the logging sites and counts the rest apart", () => {
    const hour = edgeHour([
      site("a", {
        requests: 1000,
        errorRate: 0.013,
        bytes: 5000,
        p95: 120,
        points: [{ start: at(1), total: 1000, refused: 40, failed: 13, bytes: 5000 }],
      }),
      site("b", {
        requests: 200,
        errorRate: 0,
        bytes: 100,
        p95: 412,
        complete: false,
        points: [{ start: at(1), total: 200, refused: 2, failed: 0, bytes: 100 }],
      }),
      site("c", { status: "unavailable", reason: "access_log off", requests: 0 }),
    ])
    expect(hour.sites).toBe(3)
    expect(hour.logging).toBe(2)
    expect(hour.unlogged.map((s) => s.site)).toEqual(["c"])
    expect(hour.total).toBe(1200)
    expect(hour.failed).toBe(13)
    expect(hour.refused).toBe(42)
    expect(hour.bytes).toBe(5100)
    expect(hour.perMinute).toBe(20)
    expect(hour.rate).toBeCloseTo(13 / 1200)
    expect(hour.complete).toBe(false)
    expect(hour.slowest).toEqual({ site: "b", p95: 412 })
    expect(hour.timed).toBe(2)
    expect(hour.points).toHaveLength(1)
    expect(hour.points[0].total).toBe(1200)
  })

  test("an hour with nothing logged claims no rate and no slowest site", () => {
    const hour = edgeHour([site("a", { status: "unavailable" })])
    expect(hour.total).toBe(0)
    expect(hour.rate).toBe(0)
    expect(hour.slowest).toBeUndefined()
    expect(hour.complete).toBe(true)
  })
})

describe("the failing share in words", () => {
  test("says it as the deployment pages do", () => {
    expect(failingWords(0)).toBe("No requests failed")
    expect(failingWords(0.0004)).toBe("Under 0.1% of requests failed")
    expect(failingWords(0.0104)).toBe("1.0% of requests failed")
  })
})

describe("addresses", () => {
  const q = (s) => new URLSearchParams(s)

  test("the view is ?tab=, and ?view=errors still opens the error log", () => {
    expect(trafficTab(q(""))).toBe("sites")
    expect(trafficTab(q("tab=errors"))).toBe("errors")
    expect(trafficTab(q("view=errors"))).toBe("errors")
    expect(trafficTab(q("tab=nonsense"))).toBe("sites")
  })

  test("a site's old requests address goes to its page; its error log stays here", () => {
    expect(legacySiteTarget(q(""))).toBeUndefined()
    expect(legacySiteTarget(q("site=shop.example.com"))).toBe("/proxy/sites/shop.example.com")
    expect(legacySiteTarget(q("site=shop.example.com&view=errors"))).toBeUndefined()
    expect(legacySiteTarget(q("site=docker-caddy:a b"))).toBe("/proxy/sites/docker-caddy%3Aa%20b")
  })

  test("every reading opens its site's page, an ingress route's included", () => {
    expect(readingHref({ site: "app.example.com" })).toBe("/proxy/sites/app.example.com")
  })
})

describe("the busiest sites", () => {
  test("busiest first, sites with no log last, a quiet site's hint empty", () => {
    const rows = busiestSites([
      site("quiet", { requests: 10 }),
      site("off", { status: "unavailable", reason: "access_log off" }),
      site("busy", { requests: 90, errorRate: 0.02 }),
    ])
    expect(rows.map((r) => r.key)).toEqual(["busy", "quiet", "off"])
    expect(rows[0].hint).toBe("2.0% failing")
    expect(rows[0].tone).toBe("warning")
    expect(rows[0].share).toBeCloseTo(0.9)
    expect(rows[1].hint).toBe("")
    expect(rows[2].hint).toBe("no log")
    expect(rows[2].title).toBe("access_log off")
    expect(rows[2].share).toBeUndefined()
    expect(rows[2].href).toBe("/proxy/sites/off")
  })
})
