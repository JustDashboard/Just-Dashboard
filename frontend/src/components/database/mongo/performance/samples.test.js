import { describe, expect, test } from "bun:test"
import {
  SAMPLE_CAP,
  addSample,
  latest,
  lifetimeTargeting,
  rate,
  ratio,
  runningWords,
  seriesOf,
  statRows,
  uptimeWords,
} from "./samples"

/** A `serverStatus` snapshot `seconds` after the first, with counters moved by `over`. */
const sample = (seconds, over = {}) => ({
  timestamp: 1_790_000_000_000 + seconds * 1000,
  host: "h",
  version: "7.0.0",
  process: "mongod",
  uptime: 1000 + seconds,
  storageEngine: "wiredTiger",
  topology: "standalone",
  role: "standalone",
  opcounters: { insert: 0, query: 0, update: 0, delete: 0, getmore: 0, command: 0 },
  connections: { current: 3, available: 97, active: 1, totalCreated: 10 },
  network: { bytesIn: 0, bytesOut: 0, numRequests: 0 },
  memory: { resident: 100, virtual: 200 },
  cache: { bytes: 64, maxBytes: 256, dirtyBytes: 8, pagesRead: 0, pagesWritten: 0 },
  documents: { inserted: 0, returned: 0, updated: 0, deleted: 0 },
  scanned: { keys: 0, documents: 0 },
  queue: { activeReaders: 0, activeWriters: 0, queuedReaders: 0, queuedWriters: 0 },
  latency: {
    readsMicros: 0,
    readsOps: 0,
    writesMicros: 0,
    writesOps: 0,
    commandsMicros: 0,
    commandsOps: 0,
  },
  cursors: { open: 0, timedOut: 0 },
  asserts: {},
  ...over,
})

describe("keeping samples", () => {
  test("a sample that is not newer is not a second point", () => {
    const held = addSample([], sample(0))
    expect(addSample(held, sample(0))).toBe(held)
    expect(addSample(held, sample(3))).toHaveLength(2)
  })

  test("a server that restarted starts the record again", () => {
    const held = addSample(addSample([], sample(0)), sample(3))
    const restarted = sample(6, { uptime: 2 })
    expect(addSample(held, restarted)).toEqual([restarted])
  })

  test("the record is bounded", () => {
    let held = []
    for (let n = 0; n < SAMPLE_CAP + 5; n++) held = addSample(held, sample(n * 3))
    expect(held).toHaveLength(SAMPLE_CAP)
    expect(held[0].timestamp).toBe(sample(15).timestamp)
  })
})

describe("a rate is the difference of two samples", () => {
  test("over the time between them, on the server's clock", () => {
    expect(rate(100, 160, 3)).toBe(20)
  })

  test("a counter that went down, or no time passed, says nothing", () => {
    expect(rate(100, 50, 3)).toBeNull()
    expect(rate(100, 160, 0)).toBeNull()
    expect(rate(undefined, 160, 3)).toBeNull()
  })

  test("the first row has gauges and no rates", () => {
    const [first] = statRows([sample(0)])
    expect(first).toMatchObject({ ops: null, netIn: null, connections: 3, active: 1, cache: 25 })
    expect(first.resident).toBe(100 * 1024 * 1024)
  })

  test("each kind of operation, and their sum", () => {
    const rows = statRows([
      sample(0),
      sample(3, {
        opcounters: { insert: 3, query: 30, update: 6, delete: 0, getmore: 0, command: 60 },
      }),
    ])
    expect(rows[1]).toMatchObject({
      query: 10,
      insert: 1,
      update: 2,
      delete: 0,
      command: 20,
      ops: 33,
    })
  })

  test("documents examined for each returned, only where some were returned", () => {
    const rows = statRows([
      sample(0),
      sample(3, {
        documents: { inserted: 0, returned: 30, updated: 0, deleted: 0 },
        scanned: { keys: 0, documents: 3000 },
      }),
      sample(6, {
        documents: { inserted: 0, returned: 30, updated: 0, deleted: 0 },
        scanned: { keys: 0, documents: 3300 },
      }),
    ])
    expect(rows[1].targeting).toBe(100)
    expect(rows[2].targeting).toBeNull()
    expect(seriesOf(rows, "targeting")).toEqual([100])
    expect(latest(rows, "targeting")).toBe(100)
    expect(latest(rows, "readLatency")).toBeNull()
  })

  test("the mean time of an operation in the interval", () => {
    const moved = (micros, ops) => ({
      latency: {
        readsMicros: micros,
        readsOps: ops,
        writesMicros: 0,
        writesOps: 0,
        commandsMicros: 0,
        commandsOps: 0,
      },
    })
    const rows = statRows([sample(0, moved(1000, 10)), sample(3, moved(4000, 16))])
    expect(rows[1].readLatency).toBe(500)
    // No write ran between the two: no figure, not a zero.
    expect(rows[1].writeLatency).toBeNull()
  })

  test("a storage engine that reports no cache has no cache reading", () => {
    const [row] = statRows([sample(0, { cache: null })])
    expect(row).toMatchObject({ cache: null, cacheBytes: null, dirtyBytes: null })
  })
})

describe("words", () => {
  test("query targeting since the server started", () => {
    expect(lifetimeTargeting(sample(0))).toBeNull()
    expect(
      lifetimeTargeting(
        sample(0, {
          documents: { inserted: 0, returned: 50, updated: 0, deleted: 0 },
          scanned: { keys: 0, documents: 150 },
        }),
      ),
    ).toBe(3)
  })

  test("a ratio in the fewest digits that say it", () => {
    expect(ratio(1)).toBe("1.0")
    expect(ratio(3.55)).toBe("3.5")
    expect(ratio(12.4)).toBe("12")
    expect(ratio(1500)).toBe("1,500")
  })

  test("how long something has run", () => {
    expect(runningWords(0.004)).toBe("4 ms")
    expect(runningWords(2.34)).toBe("2.3 s")
    expect(runningWords(42)).toBe("42 s")
    expect(runningWords(600)).toBe("10m")
    expect(uptimeWords(3 * 3600 + 120)).toBe("3h 2m")
    expect(uptimeWords(3 * 86400 + 4 * 3600)).toBe("3d 4h")
    expect(uptimeWords(5)).toBe("5s")
  })
})
