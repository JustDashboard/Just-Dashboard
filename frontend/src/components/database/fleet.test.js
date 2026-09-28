import { describe, expect, test } from "bun:test"
import {
  describeEdge,
  fleetConcern,
  fleetReadings,
  matchesFilter,
  matchesQuery,
  sortFleet,
  splitTopology,
  STALE_BACKUP_MS,
} from "./fleet"

const NOW = Date.parse("2026-09-25T12:00:00Z")

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
    latencyMs: 3,
    bytes: 1024,
    sizesKnown: true,
    objects: 4,
    objectWord: "tables",
    sessions: 2,
    source: "docker",
    container: "shop-db",
    exposure: "local",
    consumers: 1,
    lastBackup: "2026-09-25T00:00:00Z",
    ...overrides,
  }
}

describe("what needs attention", () => {
  test("an unreachable database is critical, whatever else is true", () => {
    expect(fleetConcern(entry({ ok: false, error: "refused" }), NOW)).toEqual({
      level: "critical",
      reason: "refused",
    })
  })
  test("a public port, a missing backup and a stale backup are warnings in that order", () => {
    expect(fleetConcern(entry({ exposure: "public" }), NOW)?.reason).toBe(
      "reachable from the internet",
    )
    expect(fleetConcern(entry({ lastBackup: undefined }), NOW)?.reason).toBe("never backed up")
    const old = new Date(NOW - STALE_BACKUP_MS - 1000).toISOString()
    expect(fleetConcern(entry({ lastBackup: old }), NOW)?.reason).toBe(
      "last backup is over a week old",
    )
  })
  test("a file needs no backup reading and a healthy database has no concern", () => {
    expect(fleetConcern(entry({ source: "file", lastBackup: undefined }), NOW)).toBeNull()
    expect(fleetConcern(entry(), NOW)).toBeNull()
  })
})

describe("the order of the cards", () => {
  test("worst first, then by name", () => {
    const sorted = sortFleet(
      [
        entry({ id: 1, name: "zeta" }),
        entry({ id: 2, name: "beta", lastBackup: undefined }),
        entry({ id: 3, name: "alpha", ok: false }),
        entry({ id: 4, name: "gamma" }),
      ],
      NOW,
    )
    expect(sorted.map((e) => e.name)).toEqual(["alpha", "beta", "gamma", "zeta"])
  })
  test("filters narrow by where the server is and by words", () => {
    const docker = entry()
    const host = entry({ source: "host", container: undefined })
    const file = entry({ source: "file" })
    const remote = entry({ source: "remote", host: "db.example.com" })
    expect(matchesFilter(docker, "docker", NOW)).toBe(true)
    expect(matchesFilter(host, "host", NOW)).toBe(true)
    expect(matchesFilter(file, "host", NOW)).toBe(true)
    expect(matchesFilter(remote, "remote", NOW)).toBe(true)
    expect(matchesFilter(docker, "attention", NOW)).toBe(false)
    expect(matchesFilter(entry({ ok: false }), "attention", NOW)).toBe(true)
    expect(matchesQuery(remote, "example")).toBe(true)
    expect(matchesQuery(docker, "shop-db")).toBe(true)
    expect(matchesQuery(docker, "nothing like it")).toBe(false)
  })
})

describe("the readings across the top", () => {
  test("sum what the fleet answered and count what is waiting", () => {
    const readings = fleetReadings(
      {
        connections: [
          entry({ id: 1 }),
          entry({ id: 2, driver: "redis", ok: false, sizesKnown: false, bytes: 0, objects: 10, sessions: 0 }),
        ],
        unreachable: [{ container: "x", driver: "mysql", reason: "no port" }],
        needsCredentials: [{ driver: "postgres", host: "127.0.0.1", port: 5432, name: "pg" }],
        checkedAt: "2026-09-25T12:00:00Z",
      },
      NOW,
    )
    expect(readings.total).toBe(2)
    expect(readings.reachable).toBe(1)
    expect(readings.bytes).toBe(1024)
    expect(readings.objects).toBe(14)
    expect(readings.sessions).toBe(2)
    expect(readings.consumers).toBe(2)
    expect(readings.attention).toBe(1)
    expect(readings.engines).toEqual(["postgres", "redis"])
    expect(readings.waiting).toBe(2)
  })
  test("an empty fleet reads as zeros", () => {
    expect(fleetReadings(undefined, NOW).total).toBe(0)
    expect(fleetReadings(undefined, NOW).sized).toBe(false)
  })
})

describe("the map's two columns", () => {
  const topology = {
    nodes: [
      { id: "db:1", kind: "database", name: "shop", product: "postgres" },
      { id: "db:2", kind: "database", name: "cache", product: "redis" },
      { id: "deploy:7", kind: "deployment", name: "api", status: "connected" },
      { id: "container:worker", kind: "container", name: "worker", status: "running" },
      { id: "host", kind: "host", name: "This server" },
    ],
    edges: [
      { from: "db:1", to: "deploy:7", via: ["binding"], sessions: 3, status: "connected" },
      { from: "db:2", to: "deploy:7", via: ["binding"], sessions: 0, status: "broken" },
      { from: "db:1", to: "container:worker", via: ["env", "network"], sessions: 0, status: "observed" },
      { from: "db:1", to: "host", via: ["session"], sessions: 1, status: "observed" },
    ],
    checkedAt: "2026-09-25T12:00:00Z",
  }
  test("databases on one side, everything else on the other, broken links first", () => {
    const { databases, consumers, feeds, fedBy } = splitTopology(topology)
    expect(databases.map((n) => n.id)).toEqual(["db:1", "db:2"])
    expect(consumers.map((n) => n.id)).toEqual(["deploy:7", "host", "container:worker"])
    expect(feeds.get("db:1")?.length).toBe(3)
    expect(fedBy.get("deploy:7")?.map((f) => f.from.name)).toEqual(["shop", "cache"])
  })
  test("a link is described by how it is known", () => {
    expect(describeEdge(topology.edges[0])).toBe("linked by its deployment · 3 open sessions")
    expect(describeEdge(topology.edges[2])).toBe("named in its environment · shares a network")
    expect(describeEdge(topology.edges[3])).toBe("1 open session")
  })
  test("nothing yet is two empty columns", () => {
    const { databases, consumers } = splitTopology(undefined)
    expect(databases).toEqual([])
    expect(consumers).toEqual([])
  })
})
