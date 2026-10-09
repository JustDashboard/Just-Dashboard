import { describe, expect, test } from "bun:test"
import { readDNSFilterView } from "./network-dns-filters"

const fingerprint = "a".repeat(64)
const observedAt = "2026-10-09T04:00:00Z"
const reading = (state = "configured") => ({
  state,
  basis: "native_configuration",
  summary: "Native metadata; client filtering is unmeasured.",
})
const connection = (engine = "adguard") => ({
  id: "dns-filter-fixture",
  name: "Fixture DNS",
  engine,
  endpoint: "https://192.0.2.10:8443",
  customCA: false,
  management: false,
  hasCredential: true,
  generation: 1,
  ownership: "connected",
  createdAt: observedAt,
  updatedAt: observedAt,
})
const section = (entries = [], state = "configured") => ({
  evidence: reading(state),
  identity: reading("redacted"),
  entries,
  ...(state === "configured" ? { fingerprint } : {}),
})
function view(engine = "adguard") {
  return {
    connection: connection(engine),
    state: "available",
    inventory: {
      engine,
      nativeVersion: engine === "technitium" ? "15.6" : "v6.7.1",
      observedAt,
      transport: reading("configured"),
      runtime: reading("unknown"),
      protection: false,
      ...(engine === "adguard" ? { filtering: false } : {}),
      sources: section([
        {
          kind: "block_subscription",
          fingerprint,
          ...(engine === "technitium" ? {} : { id: 0, enabled: false, ruleCount: 0 }),
          ...(engine === "pihole" ? { groups: [], status: 0, updatedAt: "0" } : {}),
          origin: "https://filters.example.test:8443",
        },
      ]),
      rules: section([], engine === "technitium" ? "unsupported" : "configured"),
      appRules: reading("unsupported"),
      fingerprint,
      limitations: ["Native filter contents and client decisions remain unmeasured."],
    },
  }
}
const read = (value) => readDNSFilterView(value, connection(value.connection.engine))

describe("native DNS filter inventory provenance", () => {
  test("preserves false switches, zero metadata and explicitly empty Pi-hole memberships", () => {
    const result = read(view("pihole"))
    expect(result.inventory.protection).toBe(false)
    expect(result.inventory.sources.entries[0]).toMatchObject({
      id: 0,
      enabled: false,
      groups: [],
      ruleCount: 0,
      status: 0,
      updatedAt: "0",
    })
    expect(result.inventory.runtime.state).toBe("unknown")
    expect(result.connection.management).toBe(false)
    expect(read(view()).inventory.filtering).toBe(false)
  })

  test("same-origin subscriptions retain separate native IDs and full fingerprints", () => {
    const value = view()
    value.inventory.sources.entries.push({
      ...value.inventory.sources.entries[0],
      id: 1,
      fingerprint: "b".repeat(64),
    })
    expect(read(value).inventory.sources.entries.map((row) => [row.id, row.fingerprint])).toEqual([
      [0, fingerprint],
      [1, "b".repeat(64)],
    ])
  })

  test("Technitium subscriptions retain the native 255-entry limit", () => {
    const value = view("technitium")
    const source = value.inventory.sources.entries[0]
    value.inventory.sources.entries = Array.from({ length: 255 }, () => ({ ...source }))
    expect(read(value).inventory.sources.entries).toHaveLength(255)
    value.inventory.sources.entries.push({ ...source })
    expect(() => read(value)).toThrow()
  })

  test("native unknown and unsupported sections cannot claim empty configured policy", () => {
    const value = view("technitium")
    value.inventory.sources = section([], "unknown")
    delete value.inventory.fingerprint
    value.state = "partial"
    const result = read(value)
    expect(result.inventory.sources.evidence.state).toBe("unknown")
    expect(result.inventory.sources.fingerprint).toBeUndefined()
    expect(result.inventory.rules.evidence.state).toBe("unsupported")
    value.state = "available"
    expect(() => read(value)).toThrow()
  })

  test("unavailable and unsupported native envelopes carry no invented inventory", () => {
    for (const state of ["unavailable", "unsupported"]) {
      const result = read({
        connection: connection(),
        state,
        error: "No authenticated filter metadata.",
      })
      expect(result.inventory).toBeUndefined()
      expect(result.state).toBe(state)
    }
  })

  test("a changed connection identity or trust boundary cannot borrow prior metadata", () => {
    for (const [field, next] of Object.entries({
      id: "other",
      generation: 2,
      name: "Replaced",
      endpoint: "https://192.0.2.11:8443",
      serverName: "other.example.test",
      customCA: true,
      management: true,
      hasCredential: false,
      ownership: "owned",
      containerId: "other",
      updatedAt: "2026-10-09T05:00:00Z",
    })) {
      const value = view()
      value.connection[field] = next
      expect(() => readDNSFilterView(value, connection())).toThrow("connection changed")
    }
  })

  test("redacted source origins reject credentials, private destinations and invalid URLs", () => {
    for (const origin of [
      "https://user:secret@example.test",
      "https://example.test/path",
      "https://example.test?secret=1",
      "https://example.test#secret",
      "https://example.test/",
      "file:///private/filter.txt",
      "https://example.test\\secret",
      "https://example.test\n",
      "",
      "x".repeat(513),
    ]) {
      const value = view()
      value.inventory.sources.entries[0].origin = origin
      expect(() => read(value)).toThrow()
    }
    const value = view()
    delete value.inventory.sources.entries[0].origin
    expect(read(value).inventory.sources.entries[0].origin).toBeUndefined()
  })

  test("closed native kinds and section ownership refuse mismatched engines", () => {
    for (const [engine, kind] of [
      ["adguard", "subscription_comment"],
      ["pihole", "native_rule"],
      ["technitium", "deny_regex"],
      ["adguard", "future_rule"],
    ]) {
      const value = view(engine)
      value.inventory.sources.entries[0].kind = kind
      expect(() => read(value)).toThrow()
    }
    const value = view()
    value.inventory.engine = "pihole"
    expect(() => read(value)).toThrow()
  })

  test("oversized, duplicate or missing native identities and memberships refuse rows", () => {
    for (const patch of [
      { id: -1 },
      { id: Number.MAX_SAFE_INTEGER + 1 },
      { ruleCount: 0.5 },
      { groups: [1, 1] },
      { groups: Array.from({ length: 65 }, (_, i) => i) },
      { groups: [-1] },
      { groups: [2147483648] },
      { groups: undefined },
      { enabled: undefined },
      { fingerprint: "short" },
      { status: -1 },
      { updatedAt: "bad" },
    ]) {
      const value = view("pihole")
      Object.assign(value.inventory.sources.entries[0], patch)
      expect(() => read(value)).toThrow()
    }
    const value = view()
    value.inventory.sources.entries.push({ ...value.inventory.sources.entries[0] })
    expect(() => read(value)).toThrow()
    value.inventory.sources.entries = Array.from({ length: 257 }, (_, id) => ({
      ...value.inventory.sources.entries[0],
      id,
    }))
    expect(() => read(value)).toThrow()
  })

  test("unknown DTO fields and contradictory provenance cannot silently become healthy", () => {
    const variants = [
      (v) => {
        v.secret = "unexpected"
      },
      (v) => {
        v.inventory.rules.entries.push({
          kind: "native_rule",
          fingerprint,
          text: "private contents",
        })
      },
      (v) => {
        v.inventory.sources.identity.state = "configured"
      },
      (v) => {
        v.inventory.sources.evidence.state = "unknown"
      },
      (v) => {
        delete v.inventory.sources.fingerprint
      },
      (v) => {
        delete v.inventory
      },
      (v) => {
        v.state = "verified"
      },
      (v) => {
        v.state = "partial"
      },
      (v) => {
        v.state = "unavailable"
      },
      (v) => {
        v.error = "uncertain"
      },
      (v) => {
        v.inventory.observedAt = "0"
      },
      (v) => {
        v.inventory.transport.summary = "x".repeat(2049)
      },
      (v) => {
        v.inventory.limitations = Array(33).fill("unknown")
      },
    ]
    for (const mutate of variants) {
      const value = view()
      mutate(value)
      expect(() => read(value)).toThrow()
    }
  })
})
