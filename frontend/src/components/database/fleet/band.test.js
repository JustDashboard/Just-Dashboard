import { describe, expect, test } from "bun:test"
import { DATABASE_HUES, fleetHues, fleetMarks, fleetVerdict, readerCount } from "./band"

function entry(overrides = {}) {
  return {
    id: 1,
    name: "shop",
    driver: "postgres",
    host: "127.0.0.1",
    port: "5432",
    user: "app",
    database: "shop",
    createdAt: "2026-09-01T00:00:00Z",
    ok: true,
    state: "running",
    flavor: "postgres",
    latencyMs: 2,
    bytes: 1000,
    sizesKnown: true,
    objects: 3,
    objectWord: "tables",
    sessions: 1,
    source: "docker",
    exposure: "local",
    consumers: 0,
    ...overrides,
  }
}

describe("a database's hue", () => {
  test("five databases are five hues, dealt in the order they were saved", () => {
    const fleet = [5, 1, 4, 2, 3].map((id) => entry({ id, name: `db-${id}` }))
    const hues = fleetHues(fleet)
    expect(new Set(hues.values()).size).toBe(5)
    expect(hues.get(1)).toBe(DATABASE_HUES[0])
    expect(hues.get(5)).toBe(DATABASE_HUES[4])
  })

  test("never slate, which is the bar's everything else", () => {
    expect(DATABASE_HUES).not.toContain("var(--tag-slate)")
    const hues = fleetHues(Array.from({ length: 12 }, (_, i) => entry({ id: i + 1 })))
    expect([...hues.values()]).not.toContain("var(--tag-slate)")
  })

  test("a database saved later moves nobody else's hue", () => {
    const before = fleetHues([entry({ id: 1 }), entry({ id: 2 })])
    const after = fleetHues([entry({ id: 1 }), entry({ id: 2 }), entry({ id: 9 })])
    expect(after.get(1)).toBe(before.get(1))
    expect(after.get(2)).toBe(before.get(2))
  })
})

describe("the identity line's verdict", () => {
  test("not answering outranks stopped, which outranks all clear", () => {
    expect(fleetVerdict({ total: 4, failing: 1, stopped: 1 })).toEqual({
      tone: "danger",
      label: "1 database not answering",
    })
    expect(fleetVerdict({ total: 4, failing: 0, stopped: 2 })).toEqual({
      tone: "warning",
      label: "2 databases stopped",
    })
    expect(fleetVerdict({ total: 4, failing: 0, stopped: 0 })).toEqual({
      tone: "running",
      label: "All 4 answering",
    })
  })

  test("one database is not all of one, and none is no verdict", () => {
    expect(fleetVerdict({ total: 1, failing: 0, stopped: 0 })?.label).toBe("Answering")
    expect(fleetVerdict({ total: 0, failing: 0, stopped: 0 })).toBeUndefined()
  })
})

describe("the identity line's mark", () => {
  test("each engine once, the commonest first, ties in the fleet's order, no guessed logo", () => {
    expect(fleetMarks(["redis", "postgresql", undefined, "postgresql", "mongodb"])).toEqual([
      "postgresql",
      "redis",
      "mongodb",
    ])
  })
})

describe("what the databases feed", () => {
  test("a reader two databases reach is one reader", () => {
    const topology = {
      nodes: [
        { id: "db:1", kind: "database" },
        { id: "db:2", kind: "database" },
        { id: "deploy:7", kind: "deployment" },
        { id: "host", kind: "host" },
      ],
      edges: [
        { from: "db:1", to: "deploy:7" },
        { from: "db:2", to: "deploy:7" },
        { from: "db:2", to: "host" },
      ],
    }
    expect(readerCount(topology)).toBe(2)
    expect(readerCount(undefined)).toBe(0)
  })
})
