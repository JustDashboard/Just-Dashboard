import { describe, expect, test } from "bun:test"
import { mongoConcerns, placementConcerns, ranked, redisConcerns } from "./attention"

const at = (seconds, counters = {}, gauges = {}) => ({ at: seconds * 1000, counters, gauges })
const ids = (concerns) => concerns.map((concern) => concern.id)
const now = Date.parse("2026-10-01T12:00:00Z")
const placed = (over = {}) => ({
  name: "cache",
  exposure: "local",
  dumps: true,
  managed: true,
  ...over,
})

const healthy = {
  mode: "standalone",
  memory: { used: 100, max: 0, policy: "noeviction", systemTotal: 100_000 },
  persistence: {
    rdb: { schedule: "3600 1", lastStatus: "ok", changesSinceSave: 0 },
    aof: { supported: true, enabled: false, lastWriteStatus: "ok" },
  },
  replication: { role: "primary", replicas: [] },
}

describe("what the connection's own summary gives away", () => {
  test("a port the internet can reach, and a database nobody has dumped", () => {
    const found = placementConcerns(placed({ exposure: "public" }), now)
    expect(ids(found)).toEqual(["published-everywhere", "no-backup"])
    expect(found[0].section).toBe("settings")
    expect(found[1].section).toBe("backups")
  })

  test("a recent dump is no concern; one older than a week is", () => {
    expect(placementConcerns(placed({ lastBackup: "2026-10-01T00:00:00Z" }), now)).toEqual([])
    const stale = placementConcerns(placed({ lastBackup: "2026-09-01T12:00:00Z" }), now)
    expect(ids(stale)).toEqual(["stale-backup"])
    expect(stale[0].title).toBe("The newest backup is 30 days old")
  })

  test("an engine that cannot be dumped is not told it has no backup", () => {
    expect(placementConcerns(placed({ dumps: false }), now)).toEqual([])
  })
})

describe("what a Redis server's readings say", () => {
  test("a healthy server has nothing to say", () => {
    expect(redisConcerns(healthy, [at(0, {}, { used_memory: 100, maxmemory: 0 })])).toEqual([])
    expect(redisConcerns(undefined, [])).toEqual([])
  })

  test("memory at the limit refuses writes without a policy, evicts with one", () => {
    const full = [at(0, {}, { used_memory: 95, maxmemory: 100 })]
    const [refusing] = redisConcerns(healthy, full)
    expect(refusing.id).toBe("memory-near-limit")
    expect(refusing.level).toBe("critical")
    const lru = { ...healthy, memory: { ...healthy.memory, policy: "allkeys-lru" } }
    expect(redisConcerns(lru, full)[0].level).toBe("warning")
  })

  test("a failed snapshot, keys evicted and connections refused while the page was open", () => {
    const failing = {
      ...healthy,
      persistence: {
        ...healthy.persistence,
        rdb: { schedule: "60 1", lastStatus: "err", changesSinceSave: 12 },
      },
    }
    const run = [
      at(0, { evicted_keys: 5, rejected_connections: 0 }, {}),
      at(10, { evicted_keys: 9, rejected_connections: 2 }, {}),
    ]
    expect(ids(redisConcerns(failing, run))).toEqual([
      "save-failed",
      "evicting",
      "rejecting-connections",
    ])
  })

  test("nothing saved to disk is a notice, not an alarm", () => {
    const cache = {
      ...healthy,
      persistence: {
        rdb: { schedule: "", lastStatus: "ok", changesSinceSave: 0 },
        aof: { supported: true, enabled: false },
      },
    }
    const [found] = redisConcerns(cache, [])
    expect(found.id).toBe("nothing-saved")
    expect(found.level).toBe("notice")
  })

  test("no limit matters once it holds half the machine", () => {
    const large = { ...healthy, memory: { used: 60_000, max: 0, systemTotal: 100_000 } }
    expect(ids(redisConcerns(large, []))).toEqual(["no-memory-limit"])
  })

  test("a replica that lost its primary is the worst of them", () => {
    const orphan = {
      ...healthy,
      replication: {
        role: "replica",
        replicas: [],
        primary: { addr: "10.0.0.1:6379", up: false, lastIoSecondsAgo: 40, syncing: false },
      },
    }
    expect(redisConcerns(orphan, [])[0]).toMatchObject({
      id: "replica-link-down",
      level: "critical",
    })
  })
})

describe("what a MongoDB server's readings say", () => {
  test("nothing, when nothing has crossed a line", () => {
    expect(
      mongoConcerns([at(0, {}, { connections: 3, connectionsAvailable: 97, cachePercent: 80 })]),
    ).toEqual([])
  })

  test("connections near the limit, operations queued, a cache it cannot evict from", () => {
    const run = [
      at(
        0,
        {},
        {
          connections: 96,
          connectionsAvailable: 4,
          queuedReaders: 2,
          queuedWriters: 1,
          cachePercent: 97,
        },
      ),
    ]
    const found = mongoConcerns(run)
    expect(ids(found)).toEqual(["connections-near-limit", "operations-queued", "cache-full"])
    expect(found[0].level).toBe("critical")
    expect(found[1].title).toBe("3 operations are waiting for a lock")
  })

  test("queries that read a thousand documents for each one returned", () => {
    const run = [
      at(0, { "scanned.documents": 0, "documents.returned": 0 }, {}),
      at(10, { "scanned.documents": 50_000, "documents.returned": 10 }, {}),
    ]
    expect(ids(mongoConcerns(run))).toEqual(["query-targeting"])
    expect(mongoConcerns(run.slice(0, 1))).toEqual([])
  })
})

test("the worst comes first, and equals keep the order they were found in", () => {
  const order = ranked([
    { id: "a", level: "notice" },
    { id: "b", level: "warning" },
    { id: "c", level: "critical" },
    { id: "d", level: "warning" },
  ])
  expect(ids(order)).toEqual(["c", "b", "d", "a"])
})
