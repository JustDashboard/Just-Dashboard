import { describe, expect, test } from "bun:test"
import {
  backupRunway,
  DATABASE_HUES,
  fleetHues,
  fleetMarks,
  fleetVerdict,
  readerCount,
  RUNWAY_DAYS,
  shares,
  STALE_AT,
} from "./band"

const NOW = Date.parse("2026-10-11T12:00:00Z")
const hoursAgo = (hours) => new Date(NOW - hours * 3_600_000).toISOString()

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

describe("a figure shared across the fleet", () => {
  const fleet = [
    entry({ id: 1, name: "a", bytes: 50 }),
    entry({ id: 2, name: "b", bytes: 400 }),
    entry({ id: 3, name: "c", bytes: 0 }),
    entry({ id: 4, name: "d", bytes: 100 }),
    entry({ id: 5, name: "e", sizesKnown: false }),
  ]
  const valueOf = (e) => (e.sizesKnown === false ? undefined : e.bytes)

  test("the largest first, the rest one span, a size nobody reported counted apart", () => {
    const read = shares(fleet, valueOf, 2)
    expect(read.top.map((s) => s.entry.name)).toEqual(["b", "d"])
    expect(read.total).toBe(550)
    expect(read.rest).toBe(50)
    expect(read.more).toBe(1)
    expect(read.unknown).toBe(1)
    expect(read.largest).toBe(400)
  })

  test("a zero takes no span and is not one more", () => {
    const read = shares(fleet, valueOf)
    expect(read.top.map((s) => s.entry.name)).toEqual(["b", "d", "a"])
    expect(read.more).toBe(0)
    expect(read.rest).toBe(0)
  })

  test("nothing reported is an empty bar, not a division by zero", () => {
    const read = shares([entry({ sizesKnown: false })], valueOf)
    expect(read).toMatchObject({ top: [], total: 0, rest: 0, unknown: 1, largest: 0 })
  })
})

describe("the backup runway", () => {
  const newest = new Map([
    [1, hoursAgo(2)],
    [2, hoursAgo(24 * 3)],
    [3, hoursAgo(24 * 10)],
    [5, hoursAgo(24 * 40)],
  ])
  const fleet = [
    entry({ id: 1, name: "fresh" }),
    entry({ id: 2, name: "aging" }),
    entry({ id: 3, name: "stale" }),
    entry({ id: 4, name: "never" }),
    entry({ id: 5, name: "ancient" }),
    entry({ id: 6, name: "cache", driver: "redis" }),
    entry({ id: 7, name: "gone", state: "broken", broken: true }),
  ]
  const rows = backupRunway(fleet, {
    newestOf: (e) => newest.get(e.id),
    dumps: (e) => e.driver !== "redis",
    now: NOW,
  })

  test("never first, then the oldest, and nothing an engine cannot dump or a row cannot open", () => {
    expect(rows.map((r) => r.entry.name)).toEqual(["never", "ancient", "stale", "aging", "fresh"])
  })

  test("each stands by the same day and week the readings and the cards use", () => {
    const by = Object.fromEntries(rows.map((r) => [r.entry.name, r.standing]))
    expect(by).toEqual({
      never: "never",
      ancient: "stale",
      stale: "stale",
      aging: "aging",
      fresh: "fresh",
    })
  })

  test("a dump sits where its age puts it on a fortnight, and an older one at the far end", () => {
    const at = Object.fromEntries(rows.map((r) => [r.entry.name, r.position]))
    expect(at.never).toBeUndefined()
    expect(at.ancient).toBe(1)
    expect(at.aging).toBeCloseTo(3 / RUNWAY_DAYS, 5)
    expect(STALE_AT).toBeCloseTo(7 / RUNWAY_DAYS, 5)
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
