import { describe, expect, test } from "bun:test"
import {
  alertCounts,
  areaOf,
  isAlertKind,
  judging,
  kindProduct,
  presentKinds,
  reaches,
} from "./kinds"

// Two tables in the shapes the two pages give the kit: the proxy's, whose
// engine area is drawn as whichever engine serves, and the host's, whose
// areas are fixed products or none.
const glyph = () => null
const proxy = {
  kinds: {
    cert_expiring: { label: "Certificate expiring", hint: "21, 7, 3 and 1 days left", icon: glyph },
    engine_down: { label: "Engine not running", hint: "stopped or failed", icon: glyph },
    upstream_down: { label: "Upstream down", hint: "stops answering", icon: glyph },
  },
  order: ["cert_expiring", "engine_down", "upstream_down"],
  areas: [
    {
      key: "certificates",
      title: "Certificates",
      product: () => "lets-encrypt",
      fallback: glyph,
      kinds: ["cert_expiring"],
    },
    {
      key: "engine",
      title: "The engine",
      product: (engine) => engine,
      fallback: glyph,
      kinds: ["engine_down"],
    },
    { key: "upstreams", title: "Upstreams", fallback: glyph, kinds: ["upstream_down"] },
  ],
}
const host = {
  kinds: {
    cpu_high: {
      label: "CPU high",
      hint: "Averaged over a window",
      fact: "90% or more over 10 min",
      icon: glyph,
    },
    container_down: { label: "Container down", hint: "Stopped", icon: glyph },
  },
  order: ["cpu_high", "container_down"],
  areas: [
    { key: "cpu", title: "CPU", fallback: glyph, kinds: ["cpu_high"] },
    {
      key: "containers",
      title: "Containers",
      product: "docker",
      fallback: glyph,
      kinds: ["container_down"],
    },
  ],
}

describe("a kind's area and product", () => {
  test("a fixed product, a product read from the engine, and none", () => {
    expect(kindProduct(host, "container_down")).toBe("docker")
    expect(kindProduct(host, "cpu_high")).toBeUndefined()
    expect(kindProduct(proxy, "cert_expiring", "nginx")).toBe("lets-encrypt")
    expect(kindProduct(proxy, "engine_down", "caddy")).toBe("caddy")
    expect(kindProduct(proxy, "engine_down", undefined)).toBeUndefined()
    expect(kindProduct(proxy, "upstream_down", "nginx")).toBeUndefined()
  })

  test("a kind no area names falls to the first, rather than to nothing", () => {
    expect(areaOf(host, "image_vulnerable").key).toBe("cpu")
  })

  test("a kind the server still names in its history but the table has lost is not one", () => {
    expect(isAlertKind(proxy, "engine_down")).toBe(true)
    expect(isAlertKind(proxy, "cpu_high")).toBe(false)
    expect(isAlertKind(host, "toString")).toBe(false)
  })
})

describe("who a rule tells", () => {
  const channel = (id, enabled = true) => ({ id, enabled })
  test("a rule naming no channel tells every enabled one", () => {
    const rules = [{ kind: "cpu_high", enabled: true, channels: [] }]
    expect(reaches(rules, channel(1))).toBe(true)
    expect(reaches(rules, channel(2, false))).toBe(false)
  })
  test("a paused rule tells nobody, and a named one tells only its own", () => {
    expect(reaches([{ kind: "cpu_high", enabled: false, channels: [] }], channel(1))).toBe(false)
    const named = [{ kind: "cpu_high", enabled: true, channels: [2] }]
    expect(reaches(named, channel(1))).toBe(false)
    expect(reaches(named, channel(2))).toBe(true)
  })
})

test("a muted subject is neither firing nor holding", () => {
  expect(
    alertCounts([
      { state: "firing", muted: false },
      { state: "firing", muted: true },
      { state: "pending", muted: false },
      { state: "pending", muted: true },
      { state: "quiet", muted: false },
    ]),
  ).toEqual({ firing: 1, holding: 1 })
})

describe("a kind that judges nothing on this server", () => {
  const area = { key: "certificates", kinds: ["cert_expiring", "renewal_failed"] }
  test("leaves its area the kinds that do", () => {
    expect(presentKinds(area)).toEqual(["cert_expiring", "renewal_failed"])
    expect(presentKinds(area, new Set(["renewal_failed"]))).toEqual(["cert_expiring"])
    expect(presentKinds(area, new Set(area.kinds))).toEqual([])
  })
  test("is not a rule that is on, and neither is a paused one", () => {
    const rules = [
      { kind: "engine_down", enabled: true, channels: [] },
      { kind: "watch_unreachable", enabled: true, channels: [] },
      { kind: "watch_untrusted", enabled: false, channels: [] },
    ]
    expect(judging(rules).map((rule) => rule.kind)).toEqual(["engine_down", "watch_unreachable"])
    expect(judging(rules, new Set(["engine_down"])).map((rule) => rule.kind)).toEqual([
      "watch_unreachable",
    ])
  })
})
