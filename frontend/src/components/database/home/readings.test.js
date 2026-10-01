import { describe, expect, test } from "bun:test"
import {
  ago,
  backupReading,
  clickhouseReadings,
  collectionReadings,
  compact,
  holdingsReading,
  mongoReadings,
  perSecond,
  redisReadings,
  sqlReadings,
  sqliteReadings,
} from "./readings"
import { TRANSACTIONS } from "./samples"

const at = (seconds, counters = {}, gauges = {}) => ({ at: seconds * 1000, counters, gauges })
const by = (readings) => Object.fromEntries(readings.map((reading) => [reading.key, reading]))
const TABLES = { object: "table", objects: "tables", row: "row", rows: "rows" }
const COLLECTIONS = {
  object: "collection",
  objects: "collections",
  row: "document",
  rows: "documents",
}

describe("how a figure is written", () => {
  test("every digit up to ten thousand, compact past it", () => {
    expect(compact(0)).toBe("0")
    expect(compact(9999)).toBe("9,999")
    expect(compact(16_103_894)).toBe("16.1M")
    expect(compact(28_600)).toBe("28.6K")
  })

  test("a rate keeps one decimal only while it is small", () => {
    expect(perSecond(0)).toBe("0")
    expect(perSecond(0.44)).toBe("0.4")
    expect(perSecond(3)).toBe("3")
    expect(perSecond(42.7)).toBe("43")
    expect(perSecond(12_345)).toBe("12.3K")
  })

  test("an age is measured against the newest reading", () => {
    expect(ago(0, 10_000)).toBe("just now")
    expect(ago(0, 610_000)).toBe("10m ago")
    // One unit: an age of three hours and ten seconds is three hours.
    expect(ago(0, 3 * 3600 * 1000 + 10_000)).toBe("3h ago")
    expect(ago(0, 47 * 3600 * 1000)).toBe("47h ago")
    expect(ago(0, 9 * 86_400 * 1000)).toBe("9d ago")
  })
})

describe("a SQL server's readings", () => {
  const sessions = { sessions: 12, sessionsActive: 2, sessionsIdle: 10, sessionsMax: 100 }
  const run = [
    at(
      0,
      { [TRANSACTIONS]: 100, blocksHit: 900, blocksRead: 100, rowsRead: 0, rowsWritten: 0 },
      {
        ...sessions,
        databaseBytes: 1024 * 1024,
      },
    ),
    at(
      5,
      { [TRANSACTIONS]: 150, blocksHit: 990, blocksRead: 110, rowsRead: 500, rowsWritten: 5 },
      {
        ...sessions,
        databaseBytes: 1024 * 1024 + 2048,
      },
    ),
  ]

  test("sessions against the limit, with a meter", () => {
    const { sessions: tile } = by(sqlReadings(run))
    expect(tile.value).toBe("12")
    expect(tile.trailing).toBe("of 100")
    expect(tile.meter).toBe(12)
    expect(tile.tone).toBe("default")
    expect(tile.hint).toBe("2 active · 10 idle")
  })

  test("sessions near the limit take the utilisation tone, and waiting ones are said first", () => {
    const busy = [
      at(0, {}, { sessions: 95, sessionsActive: 90, sessionsWaiting: 4, sessionsMax: 100 }),
    ]
    const { sessions: tile } = by(sqlReadings(busy))
    expect(tile.tone).toBe("danger")
    expect(tile.hint).toBe("90 active · 4 waiting on a lock")
  })

  test("an engine with no limit has no meter", () => {
    const { sessions: tile } = by(sqlReadings([at(0, {}, { sessions: 3, sessionsMax: 0 })]))
    expect(tile.trailing).toBe("open")
    expect(tile.meter).toBeUndefined()
  })

  test("sessions the account may not read are a dash, not a zero", () => {
    const { sessions: tile } = by(sqlReadings([at(0, {}, {})]))
    expect(tile.value).toBeUndefined()
    expect(tile.hint).toBe("Not reported to this account")
  })

  test("transactions are a rate from the second sample on", () => {
    const first = by(sqlReadings(run.slice(0, 1))).transactions
    expect(first.value).toBeUndefined()
    expect(first.hint).toBe("The rate needs a second reading")
    const { transactions } = by(sqlReadings(run))
    expect(transactions.value).toBe("10")
    expect(transactions.trailing).toBe("a second")
    expect(transactions.trend.values).toEqual([10])
    expect(transactions.hint).toBe("100 rows read · 1 written")
  })

  test("the cache is the window's share, under ninety is a warning", () => {
    const { cache } = by(sqlReadings(run))
    expect(cache.value).toBe("90.0%")
    expect(cache.meter).toBe(90)
    expect(cache.tone).toBe("default")
    const cold = [at(0, { blocksHit: 10, blocksRead: 90 }, {})]
    const tile = by(sqlReadings(cold)).cache
    expect(tile.value).toBe("10.0%")
    expect(tile.tone).toBe("warning")
    expect(tile.hint).toBe("of block reads since the server started")
  })

  test("size says how far it moved while the page was open", () => {
    expect(by(sqlReadings(run)).size.value).toBe("1.0 MB")
    expect(by(sqlReadings(run)).size.hint).toBe("+2.0 KB since this page was opened")
    expect(by(sqlReadings(run.slice(0, 1))).size.hint).toBe("on disk")
  })
})

describe("the engines that read differently", () => {
  test("SQLite is its file", () => {
    const run = [
      at(
        0,
        {},
        {
          fileBytes: 319488,
          walBytes: 0,
          pageSize: 4096,
          pageCount: 78,
          freelistPages: 39,
          reclaimableBytes: 159744,
          tables: 3,
          indexes: 1,
        },
      ),
    ]
    const tiles = by(
      sqliteReadings(run, {
        facts: { journalMode: "delete", synchronous: "full", encoding: "UTF-8" },
      }),
    )
    expect(tiles.file.value).toBe("312.0 KB")
    expect(tiles.file.hint).toBe("3 tables · 1 index")
    expect(tiles.wal.hint).toBe("no write-ahead log in delete mode")
    expect(tiles.pages.value).toBe("78")
    expect(tiles.pages.trailing).toBe("× 4 KB")
    expect(tiles.free.meter).toBe(50)
    expect(tiles.free.tone).toBe("warning")
    expect(tiles.journal.value).toBe("DELETE")
  })

  test("ClickHouse reads the machine under it", () => {
    const run = [
      at(0, { queries: 0, insertedRows: 0, selectedRows: 0, failedQueries: 0 }, {}),
      at(
        10,
        { queries: 20, insertedRows: 1000, selectedRows: 50, failedQueries: 0 },
        {
          runningQueries: 1,
          totalParts: 74,
          maxPartsPerPartition: 7,
          memoryResident: 1024 ** 3,
          hostMemoryBytes: 4 * 1024 ** 3,
          diskFreeBytes: 10 * 1024 ** 3,
          diskTotalBytes: 100 * 1024 ** 3,
        },
      ),
    ]
    const tiles = by(clickhouseReadings(run))
    expect(tiles.queries.value).toBe("2")
    expect(tiles.inserted.value).toBe("100")
    expect(tiles.parts.hint).toBe("at most 7 in one partition")
    expect(tiles.memory.meter).toBe(25)
    expect(tiles.disk.value).toBe("10.0 GB")
    expect(tiles.disk.meter).toBe(90)
    expect(tiles.disk.tone).toBe("danger")
  })

  test("Redis without a limit measures against the machine and takes no tone", () => {
    const run = [
      at(0, { total_commands_processed: 0, keyspace_hits: 0, keyspace_misses: 0 }, {}),
      at(
        5,
        { total_commands_processed: 500, keyspace_hits: 3, keyspace_misses: 1 },
        {
          used_memory: 950,
          maxmemory: 0,
          connected_clients: 4,
          blocked_clients: 0,
          keys: 28600,
          expires: 20500,
          rdb_last_save_time: 2,
          rdb_changes_since_last_save: 9,
        },
      ),
    ]
    const server = { memory: { systemTotal: 1000 }, persistence: { rdb: { lastStatus: "ok" } } }
    const tiles = by(redisReadings(run, server))
    expect(tiles.ops.value).toBe("100")
    expect(tiles.memory.meter).toBe(95)
    expect(tiles.memory.tone).toBe("default")
    expect(tiles.memory.hint).toContain("no limit set")
    expect(tiles.hits.value).toBe("75.0%")
    expect(tiles.keys.value).toBe("28.6K")
    expect(tiles.saved.value).toBe("just now")
    expect(tiles.saved.hint).toBe("9 changes since")
  })

  test("Redis with a limit is toned against it, and a failed save is said", () => {
    const run = [at(5, {}, { used_memory: 950, maxmemory: 1000, rdb_last_save_time: 0 })]
    const server = {
      memory: { policy: "allkeys-lru" },
      persistence: { rdb: { lastStatus: "err" } },
    }
    const tiles = by(redisReadings(run, server))
    expect(tiles.memory.trailing).toBe("of 1000 B")
    expect(tiles.memory.tone).toBe("danger")
    expect(tiles.memory.hint).toBe("policy allkeys-lru")
    expect(tiles.saved.value).toBe("Never")
    expect(tiles.saved.tone).toBe("danger")
  })

  test("MongoDB: connections against what is left; the cache warns only when it cannot evict", () => {
    const run = [
      at(
        0,
        {
          "ops.query": 0,
          "ops.insert": 0,
          "ops.update": 0,
          "ops.delete": 0,
          "ops.getmore": 0,
          "ops.command": 0,
        },
        {},
      ),
      at(
        10,
        {
          "ops.query": 20,
          "ops.insert": 10,
          "ops.update": 0,
          "ops.delete": 0,
          "ops.getmore": 0,
          "ops.command": 70,
        },
        {
          connections: 90,
          connectionsAvailable: 10,
          connectionsActive: 5,
          cacheBytes: 80,
          cacheMaxBytes: 100,
          cachePercent: 80,
          cacheDirtyBytes: 0,
        },
      ),
    ]
    const tiles = by(mongoReadings(run))
    expect(tiles.ops.value).toBe("10")
    expect(tiles.ops.hint).toBe("2 reads · 1 writes")
    expect(tiles.connections.trailing).toBe("of 100")
    expect(tiles.connections.tone).toBe("danger")
    expect(tiles.cache.meter).toBe(80)
    expect(tiles.cache.tone).toBe("default")
  })
})

describe("what a database holds", () => {
  test("counted in the engine's words, with a plus when more exist", () => {
    const tile = holdingsReading(TABLES, { objects: 200, more: true, rows: 89_100 })
    expect(tile.label).toBe("Tables")
    expect(tile.value).toBe("200+")
    expect(tile.hint).toBe("about 89.1K rows")
  })

  test("not read yet holds its place; failed says so", () => {
    expect(holdingsReading(TABLES, undefined).pending).toBe(true)
    expect(holdingsReading(TABLES, undefined, "timeout").hint).toBe("Could not be counted")
  })

  test("collections leave the system ones out and sum what was measured", () => {
    const [size, count] = collectionReadings(COLLECTIONS, {
      database: "app",
      statsTruncated: false,
      collections: [
        {
          name: "orders",
          system: false,
          statsKnown: true,
          count: 6000,
          size: 2048,
          storageSize: 1024,
          indexSize: 1024,
        },
        {
          name: "active",
          type: "view",
          system: false,
          statsKnown: false,
          count: 0,
          size: 0,
          storageSize: 0,
          indexSize: 0,
        },
        {
          name: "system.views",
          system: true,
          statsKnown: true,
          count: 1,
          size: 9,
          storageSize: 9,
          indexSize: 9,
        },
      ],
    })
    expect(size.value).toBe("2.0 KB")
    expect(count.value).toBe("2")
    expect(count.hint).toBe("about 6,000 documents")
  })
})

describe("when it was last backed up", () => {
  const now = Date.parse("2026-10-01T12:00:00Z")

  test("never is a warning", () => {
    const tile = backupReading({}, now)
    expect(tile.value).toBe("Never")
    expect(tile.tone).toBe("warning")
  })

  test("the newest dump's own facts are the hint", () => {
    const tile = backupReading(
      {
        newest: {
          file: "a.dump",
          size: 2048,
          takenAt: "2026-10-01T09:00:00Z",
          format: "pg_dump archive",
          tool: "pg_dump",
        },
      },
      now,
    )
    expect(tile.value).toBe("3h ago")
    expect(tile.tone).toBe("default")
    expect(tile.hint).toBe("2.0 KB · pg_dump")
  })

  test("older than a week is a warning; a dump in flight is said", () => {
    const stale = backupReading({ lastBackup: "2026-09-20T12:00:00Z", running: true }, now)
    expect(stale.tone).toBe("warning")
    expect(stale.hint).toBe("A dump is being taken now")
  })
})
