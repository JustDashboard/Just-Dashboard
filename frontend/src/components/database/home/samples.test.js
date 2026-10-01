import { describe, expect, test } from "bun:test"
import {
  TRANSACTIONS,
  WINDOW,
  chartRows,
  drawn,
  gauge,
  heldSeconds,
  heldShare,
  lastRate,
  mongoSample,
  pushSample,
  rateBetween,
  rates,
  redisSample,
  shareBetween,
  sourced,
  sqlSample,
} from "./samples"

const at = (seconds, counters = {}, gauges = {}) => ({ at: seconds * 1000, counters, gauges })

describe("the run of samples a home holds", () => {
  test("keeps the newest sixty", () => {
    let held = []
    for (let i = 0; i < WINDOW + 5; i++) held = pushSample(held, at(i))
    expect(held).toHaveLength(WINDOW)
    expect(held[0].at).toBe(5000)
    expect(held[WINDOW - 1].at).toBe((WINDOW + 4) * 1000)
  })

  test("a reading that is not newer than the last is not a reading", () => {
    const held = pushSample([], at(10))
    expect(pushSample(held, at(10))).toBe(held)
    expect(pushSample(held, at(9))).toBe(held)
  })
})

describe("a rate is two totals and the clock between them", () => {
  test("divides the difference by the seconds the answers state", () => {
    expect(rateBetween(at(0, { queries: 100 }), at(5, { queries: 150 }), "queries")).toBe(10)
  })

  test("sums several counters into one rate", () => {
    const a = at(0, { commits: 10, rollbacks: 1 })
    const b = at(2, { commits: 16, rollbacks: 3 })
    expect(rateBetween(a, b, ["commits", "rollbacks"])).toBe(4)
  })

  test("a total that went down is a restart, not a negative rate", () => {
    expect(rateBetween(at(0, { queries: 900 }), at(5, { queries: 12 }), "queries")).toBeUndefined()
  })

  test("a counter the engine does not keep has no rate", () => {
    expect(rateBetween(at(0, {}), at(5, { queries: 1 }), "queries")).toBeUndefined()
    expect(rateBetween(at(0, { a: 1 }), at(5, { a: 2 }), ["a", "b"])).toBeUndefined()
  })

  test("a clock that did not move has no rate", () => {
    expect(rateBetween(at(5, { queries: 1 }), at(5, { queries: 9 }), "queries")).toBeUndefined()
  })

  test("one rate per interval, with the restart left as a hole", () => {
    const run = [at(0, { q: 0 }), at(5, { q: 50 }), at(10, { q: 5 }), at(15, { q: 25 })]
    expect(rates(run, "q")).toEqual([10, undefined, 4])
    expect(drawn(rates(run, "q"))).toEqual([10, 4])
    expect(lastRate(run, "q")).toBe(4)
    expect(lastRate(run.slice(0, 1), "q")).toBeUndefined()
  })
})

describe("a hit rate", () => {
  test("is the hits' share of what moved in the interval", () => {
    const a = at(0, { hit: 100, miss: 10 })
    const b = at(5, { hit: 190, miss: 20 })
    expect(shareBetween(a, b, "hit", "miss")).toBe(90)
  })

  test("an interval in which nothing was asked has none — it is not zero", () => {
    const a = at(0, { hit: 100, miss: 10 })
    expect(shareBetween(a, at(5, { hit: 100, miss: 10 }), "hit", "miss")).toBeUndefined()
  })

  test("over the window while something moved, over the server's life when nothing did", () => {
    const moving = [at(0, { hit: 0, miss: 0 }), at(5, { hit: 3, miss: 1 })]
    expect(heldShare(moving, "hit", "miss")).toEqual({ value: 75, over: "window" })
    const idle = [at(0, { hit: 80, miss: 20 }), at(5, { hit: 80, miss: 20 })]
    expect(heldShare(idle, "hit", "miss")).toEqual({ value: 80, over: "lifetime" })
    expect(heldShare([at(0, { hit: 0, miss: 0 })], "hit", "miss")).toBeUndefined()
    expect(heldShare([], "hit", "miss")).toBeUndefined()
  })
})

describe("the rows a chart draws", () => {
  const run = [
    at(0, { q: 0, hit: 0, miss: 0 }, { open: 3 }),
    at(5, { q: 50, hit: 9, miss: 1 }, { open: 4 }),
    at(10, { q: 10, hit: 9, miss: 1 }, {}),
  ]
  const sources = [
    { key: "open", gauge: "open" },
    { key: "rate", rate: "q" },
    { key: "hit", share: ["hit", "miss"] },
  ]

  test("one row per sample; a rate belongs to the interval that ends at it", () => {
    const rows = chartRows(run, sources)
    expect(rows).toEqual([
      { ts: 0, open: 3 },
      { ts: 5000, open: 4, rate: 10, hit: 90 },
      // The total went down, nothing was looked up, and the gauge was not
      // reported: three holes, none of them a zero.
      { ts: 10000 },
    ])
  })

  test("a source is offered only where the newest sample carries it", () => {
    expect(sourced(run[1], sources[0])).toBe(true)
    expect(sourced(run[2], sources[0])).toBe(false)
    expect(sourced(run[1], { key: "x", rate: "absent" })).toBe(false)
    expect(sourced(undefined, sources[0])).toBe(false)
  })

  test("how long the run covers", () => {
    expect(heldSeconds(run)).toBe(10)
    expect(heldSeconds(run.slice(0, 1))).toBe(0)
  })
})

describe("each engine's answer as a sample", () => {
  test("a SQL snapshot carries its sessions as gauges and its transactions under one name", () => {
    const sample = sqlSample({
      supported: true,
      at: "2026-10-01T10:00:00Z",
      driver: "postgres",
      databaseBytes: 2048,
      uptimeSeconds: 60,
      connections: { total: 4, active: 1, idle: 3, idleInTransaction: 0, waiting: 0, max: 100 },
      counters: { transactionsCommitted: 10, transactionsRolledBack: 2, blocksHit: 5 },
      gauges: { replicas: 0 },
      pool: null,
    })
    expect(sample.at).toBe(Date.parse("2026-10-01T10:00:00Z"))
    expect(sample.counters[TRANSACTIONS]).toBe(12)
    expect(gauge([sample], "sessions")).toBe(4)
    expect(gauge([sample], "sessionsMax")).toBe(100)
    expect(gauge([sample], "databaseBytes")).toBe(2048)
  })

  test("an engine that counts every transaction in one total is read from that total", () => {
    const sample = sqlSample({
      supported: true,
      at: "2026-10-01T10:00:00Z",
      driver: "sqlserver",
      databaseBytes: 1,
      counters: { transactions: 488, transactionsCommitted: 33 },
      gauges: {},
      pool: null,
    })
    expect(sample.counters[TRANSACTIONS]).toBe(488)
  })

  test("a snapshot with no sessions and no counters is still a sample (SQLite)", () => {
    const sample = sqlSample({
      supported: true,
      at: "2026-10-01T10:00:00Z",
      driver: "sqlite",
      databaseBytes: 4096,
      counters: {},
      gauges: { pageCount: 1 },
      pool: null,
    })
    expect(sample.counters).toEqual({})
    expect(gauge([sample], "sessions")).toBeUndefined()
    expect(gauge([sample], "pageCount")).toBe(1)
  })

  test("a snapshot the engine refused carries no size: none is not zero bytes", () => {
    const sample = sqlSample({
      supported: false,
      reason: "permission denied",
      at: "2026-10-01T10:00:00Z",
      driver: "postgres",
      databaseBytes: 0,
      counters: {},
      gauges: {},
      pool: null,
    })
    expect(gauge([sample], "databaseBytes")).toBeUndefined()
  })

  test("an answer with no clock is not a sample", () => {
    expect(sqlSample({ at: "", counters: {}, gauges: {}, databaseBytes: 0 })).toBeUndefined()
    expect(redisSample({ server: {} })).toBeUndefined()
    // A list where a report was expected: not a sample, and not a thrown render.
    expect(sqlSample([])).toBeUndefined()
    expect(redisSample([])).toBeUndefined()
    expect(mongoSample([])).toBeUndefined()
    expect(mongoSample({ server: {} })).toBeUndefined()
  })

  test("Redis: the totals that only grow are counters, the rest are readings of now", () => {
    const sample = redisSample({
      server: {
        sampledAtMs: 1000,
        counters: {
          total_commands_processed: 50,
          keyspace_hits: 4,
          used_memory: 2048,
          connected_clients: 3,
          // A flavour that does not keep a figure reports it as -1.
          evicted_keys: -1,
        },
      },
    })
    expect(sample.counters).toEqual({ total_commands_processed: 50, keyspace_hits: 4 })
    expect(sample.gauges).toEqual({ used_memory: 2048, connected_clients: 3 })
  })

  test("MongoDB: serverStatus is flattened, memory is in bytes, the cache is a share", () => {
    const sample = mongoSample({
      server: {
        timestamp: 5000,
        uptime: 30,
        opcounters: { insert: 1, query: 2, command: 3 },
        connections: { current: 3, available: 97, active: 1 },
        network: { bytesIn: 10, bytesOut: 20, numRequests: 5 },
        mem: { resident: 2 },
        cache: { bytes: 50, maxBytes: 200, dirtyBytes: 0 },
      },
    })
    expect(sample.at).toBe(5000)
    expect(sample.counters["ops.query"]).toBe(2)
    expect(sample.counters["network.in"]).toBe(10)
    expect(sample.gauges.connections).toBe(3)
    expect(sample.gauges.cachePercent).toBe(25)
    expect(sample.gauges.residentBytes).toBe(2 * 1024 * 1024)
  })

  test("MongoDB without a cache reports none, rather than an empty one", () => {
    const sample = mongoSample({ server: { timestamp: 1, opcounters: {} } })
    expect(sample.gauges.cachePercent).toBeUndefined()
    expect(sample.gauges.cacheBytes).toBeUndefined()
  })
})
