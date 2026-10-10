import { describe, expect, test } from "bun:test"
import {
  backupReadings,
  concernGroups,
  concernLevel,
  describeEdge,
  feedsByConnection,
  fleetConcerns,
  fleetReadings,
  groupFleet,
  matchesQuery,
  nextSort,
  powerOffers,
  pushSample,
  reachWord,
  shortEdge,
  sortFleet,
  sortRows,
  splitTopology,
  STALE_BACKUP_MS,
  storedBytes,
  topologyReadings,
  whereWord,
  worstStatus,
} from "./fleet"

const NOW = Date.parse("2026-09-25T12:00:00Z")

function entry(overrides = {}) {
  return {
    id: 1,
    name: "shop",
    driver: "postgres",
    flavor: "postgres",
    host: "127.0.0.1",
    port: "5432",
    user: "app",
    database: "shop",
    createdAt: "2026-09-01T00:00:00Z",
    environment: "",
    readOnly: false,
    notes: "",
    origin: "",
    ok: true,
    state: "running",
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

const kinds = (concerns) => concerns.map((concern) => concern.kind)

describe("what needs attention", () => {
  test("a healthy database with a fresh dump has no concern", () => {
    expect(fleetConcerns(entry(), { now: NOW })).toEqual([])
  })

  test("every concern is listed, worst first: a public port does not hide a missing backup", () => {
    const concerns = fleetConcerns(
      entry({ ok: false, state: "unreachable", exposure: "public", lastBackup: undefined }),
      { now: NOW },
    )
    expect(kinds(concerns)).toEqual(["unreachable", "public", "never-backed-up"])
    expect(concerns[0].level).toBe("critical")
    expect(concerns[1].level).toBe("warning")
  })

  test("a server somebody stopped has not failed, and matters only when something uses it", () => {
    expect(
      kinds(fleetConcerns(entry({ ok: false, state: "stopped", consumers: 0 }), { now: NOW })),
    ).toEqual([])
    expect(
      kinds(fleetConcerns(entry({ ok: false, state: "stopped", consumers: 2 }), { now: NOW })),
    ).toEqual(["stopped"])
    expect(kinds(fleetConcerns(entry({ ok: false, state: "paused" }), { now: NOW }))).toEqual([
      "paused",
    ])
  })

  test("a row that cannot be opened says only that", () => {
    const concerns = fleetConcerns(
      entry({
        ok: false,
        state: "broken",
        broken: true,
        exposure: "unknown",
        lastBackup: undefined,
      }),
      { now: NOW },
    )
    expect(kinds(concerns)).toEqual(["broken"])
  })

  test("a stale dump weighs what a missing one does, and the dump directories win over the fleet's own date", () => {
    const old = new Date(NOW - STALE_BACKUP_MS - 1000).toISOString()
    expect(kinds(fleetConcerns(entry({ lastBackup: old }), { now: NOW }))).toEqual(["stale-backup"])
    // One weight for the list of findings, the card and the table.
    expect(fleetConcerns(entry({ lastBackup: old }), { now: NOW })[0].level).toBe("warning")
    expect(
      kinds(
        fleetConcerns(entry({ lastBackup: old }), {
          now: NOW,
          backup: { newest: "2026-09-25T11:00:00Z", running: false },
        }),
      ),
    ).toEqual([])
    expect(
      kinds(fleetConcerns(entry(), { now: NOW, backup: { newest: undefined, running: false } })),
    ).toEqual(["never-backed-up"])
  })

  test("an engine nothing can dump is not 'never backed up'", () => {
    expect(fleetConcerns(entry({ lastBackup: undefined }), { now: NOW, dumps: false })).toEqual([])
  })

  test("a fact is said at the level of the concern it is about", () => {
    const concerns = fleetConcerns(entry({ exposure: "public", lastBackup: undefined }), {
      now: NOW,
    })
    expect(concernLevel(concerns, "public")).toBe("warning")
    expect(concernLevel(concerns, "stale-backup", "never-backed-up")).toBe("warning")
    expect(concernLevel(concerns, "unreachable")).toBeUndefined()
    expect(concernLevel([], "public")).toBeUndefined()
  })
})

describe("the attention list's findings", () => {
  const concernsOf = (one) => fleetConcerns(one, { now: NOW })
  const open = (id) => entry({ id, name: `db${id}`, exposure: "public" })

  test("two databases with one concern are two findings; more are one that names them", () => {
    const two = concernGroups([open(1), open(2)], concernsOf)
    expect(two).toHaveLength(1)
    expect(two[0]).toMatchObject({ kind: "public", grouped: false })
    const many = concernGroups([open(1), open(2), open(3), open(4)], concernsOf)
    expect(many[0].grouped).toBe(true)
    expect(many[0].entries.map((one) => one.name)).toEqual(["db1", "db2", "db3", "db4"])
  })

  test("forty databases are a handful of findings, worst first", () => {
    const fleet = Array.from({ length: 40 }, (_, index) =>
      entry({
        id: index + 1,
        name: `db${index + 1}`,
        ok: index % 10 !== 0,
        state: index % 10 === 0 ? "unreachable" : "running",
        exposure: index % 5 === 1 ? "public" : "local",
        lastBackup: index % 2 ? undefined : "2026-09-25T00:00:00Z",
      }),
    )
    const groups = concernGroups(fleet, concernsOf)
    expect(groups.map((group) => group.kind)).toEqual(["unreachable", "public", "never-backed-up"])
    expect(groups.every((group) => group.grouped)).toBe(true)
    expect(groups.map((group) => group.entries.length)).toEqual([4, 8, 20])
  })

  test("a database with two concerns stands in both", () => {
    const groups = concernGroups([entry({ exposure: "public", lastBackup: undefined })], concernsOf)
    expect(groups.map((group) => group.kind)).toEqual(["public", "never-backed-up"])
  })
})

describe("what a database holds", () => {
  const file = entry({ id: 7, name: "notes", sizesKnown: false, bytes: 0 })

  test("is what its engine reported, else what discovery measured, else unknown", () => {
    expect(storedBytes(entry({ bytes: 2048 }))).toBe(2048)
    expect(storedBytes(file)).toBeUndefined()
    expect(storedBytes(file, new Map([[7, 319_488]]))).toBe(319_488)
    // The engine's own figure wins over a measurement of something else.
    expect(storedBytes(entry({ id: 7, bytes: 10 }), new Map([[7, 99]]))).toBe(10)
  })

  test("the table uses discovery's measurement when the engine reports no size", () => {
    const measured = new Map([[7, 4096]])
    const storedOf = (one) => storedBytes(one, measured)
    const list = [entry({ id: 1, name: "shop", bytes: 1024 }), file]
    const readings = fleetReadings({ connections: list }, { now: NOW, storedOf })
    expect(readings.bytes).toBe(5120)
    expect(readings.sized).toBe(2)
    expect(
      sortRows(list, { key: "size", dir: "desc" }, () => undefined, storedOf).map(
        (one) => one.name,
      ),
    ).toEqual(["notes", "shop"])
  })
})

describe("the dump readings", () => {
  test("the summary's newest dump and its running job, with the fleet's date as the fallback", () => {
    const fleet = { connections: [entry({ id: 1 }), entry({ id: 2, lastBackup: undefined })] }
    const readings = backupReadings(fleet, {
      connections: [
        { id: 2, name: "b", count: 1, totalSize: 9, newest: { takenAt: "2026-09-24T00:00:00Z" } },
        { id: 3, name: "c", count: 0, totalSize: 0, newest: null, job: { status: "running" } },
      ],
    })
    expect(readings.get(1)).toEqual({ newest: "2026-09-25T00:00:00Z", running: false })
    expect(readings.get(2)).toEqual({ newest: "2026-09-24T00:00:00Z", running: false })
    expect(readings.get(3)).toEqual({ newest: undefined, running: true })
    expect(backupReadings(undefined, undefined).size).toBe(0)
  })
})

describe("the order of the cards", () => {
  test("worst first, then by name", () => {
    const sorted = sortFleet(
      [
        entry({ id: 1, name: "zeta" }),
        entry({ id: 2, name: "beta", exposure: "public" }),
        entry({ id: 3, name: "alpha", ok: false, state: "unreachable" }),
        entry({ id: 4, name: "acme" }),
      ],
      (one) => fleetConcerns(one, { now: NOW }),
    )
    expect(sorted.map((one) => one.name)).toEqual(["alpha", "beta", "acme", "zeta"])
  })
})

describe("the fleet's search", () => {
  test("words match the name, the engine's label, the container and the environment", () => {
    const one = entry({ flavor: "mariadb", environment: "staging" })
    expect(matchesQuery(one, "")).toBe(true)
    expect(matchesQuery(one, "SHOP-DB")).toBe(true)
    expect(matchesQuery(one, "maria", "MariaDB")).toBe(true)
    expect(matchesQuery(one, "staging")).toBe(true)
    expect(matchesQuery(one, "redis")).toBe(false)
  })

  test("the address a card prints finds it: the port, and the host with it", () => {
    const one = entry({ host: "127.0.0.1", port: "55432" })
    expect(matchesQuery(one, "55432")).toBe(true)
    expect(matchesQuery(one, "127.0.0.1:55432")).toBe(true)
    expect(matchesQuery(one, "55433")).toBe(false)
  })
})

describe("the shelves", () => {
  const list = [
    entry({ id: 1, name: "a", source: "remote", environment: "production" }),
    entry({ id: 2, name: "b", source: "docker", environment: "" }),
    entry({ id: 3, name: "c", source: "file", driver: "sqlite", environment: "Production" }),
    entry({ id: 4, name: "d", source: "docker", driver: "redis", environment: "staging" }),
  ]
  const label = (one) => ({ postgres: "PostgreSQL", redis: "Redis", sqlite: "SQLite" })[one.driver]

  test("by place, in a fixed order, leaving out the empty ones", () => {
    const groups = groupFleet(list, "place", label)
    expect(groups.map((g) => [g.label, g.entries.map((e) => e.name)])).toEqual([
      ["Containers", ["b", "d"]],
      ["Files", ["c"]],
      ["Elsewhere", ["a"]],
    ])
  })

  test("by engine, in name order", () => {
    expect(groupFleet(list, "engine", label).map((g) => g.label)).toEqual([
      "PostgreSQL",
      "Redis",
      "SQLite",
    ])
  })

  test("by environment, whatever its case, with the unlabelled last", () => {
    const groups = groupFleet(list, "environment", label)
    expect(groups.map((g) => [g.label, g.entries.length])).toEqual([
      ["production", 2],
      ["staging", 1],
      ["No environment", 1],
    ])
  })
})

describe("the words for where and how far", () => {
  test("where a server runs is the literal that tells it from its neighbours", () => {
    expect(whereWord(entry())).toEqual({ label: "Container", text: "shop-db", mono: true })
    expect(
      whereWord(entry({ source: "host", container: undefined, unit: "postgresql.service" })).text,
    ).toBe("postgresql.service")
    expect(whereWord(entry({ source: "host", container: undefined }))).toEqual({
      label: "Listens on",
      text: "127.0.0.1:5432",
      mono: true,
    })
    expect(whereWord(entry({ source: "file", database: "/srv/a.db", port: "" })).text).toBe(
      "/srv/a.db",
    )
    expect(whereWord(entry({ source: "remote", host: "db.example.com" })).text).toBe(
      "db.example.com:5432",
    )
  })

  test("reach is said in words", () => {
    expect(reachWord("local")).toBe("this server only")
    expect(reachWord("public")).toBe("the internet")
  })
})

describe("which power actions a list offers", () => {
  test("a container that is up can be stopped or restarted; one that is down, started", () => {
    expect(powerOffers(entry())).toEqual({ start: false, stop: true, restart: true })
    expect(powerOffers(entry({ ok: false, state: "stopped" }))).toEqual({
      start: true,
      stop: false,
      restart: false,
    })
    expect(powerOffers(entry({ ok: false, state: "paused" }))).toEqual({
      start: false,
      stop: true,
      restart: false,
    })
  })

  test("a remote server, a file and a native server with no unit offer nothing", () => {
    const none = { start: false, stop: false, restart: false }
    expect(powerOffers(entry({ source: "remote", container: undefined }))).toEqual(none)
    expect(powerOffers(entry({ source: "file", container: undefined }))).toEqual(none)
    expect(powerOffers(entry({ source: "host", container: undefined }))).toEqual(none)
    expect(
      powerOffers(entry({ source: "host", container: undefined, unit: "redis.service" })).stop,
    ).toBe(true)
  })
})

describe("the readings across the top", () => {
  test("count what runs, sum what answered, and say how many were read", () => {
    const fleet = {
      connections: [
        entry({ id: 1, bytes: 1000, sessions: 3 }),
        entry({ id: 2, bytes: 500, sessions: 1, lastBackup: undefined }),
        entry({ id: 3, ok: false, state: "stopped", bytes: 0, sizesKnown: false, sessions: 0 }),
        entry({ id: 4, ok: false, state: "unreachable", bytes: 0, sizesKnown: false, sessions: 0 }),
      ],
    }
    const readings = fleetReadings(fleet, { now: NOW })
    expect(readings.total).toBe(4)
    expect(readings.running).toBe(2)
    expect(readings.stopped).toBe(1)
    expect(readings.failing).toBe(1)
    expect(readings.bytes).toBe(1500)
    expect(readings.sized).toBe(2)
    expect(readings.sessions).toBe(4)
    expect(readings.answering).toBe(2)
    expect(readings.dumpable).toBe(4)
    expect(readings.fresh).toBe(3)
    expect(readings.never).toBe(1)
  })

  test("what cannot be dumped or opened is not counted against the backups", () => {
    const fleet = {
      connections: [
        entry({ id: 1 }),
        entry({ id: 2, driver: "x", lastBackup: undefined }),
        entry({ id: 3, broken: true, state: "broken", ok: false, lastBackup: undefined }),
      ],
    }
    const readings = fleetReadings(fleet, { now: NOW, dumps: (one) => one.driver !== "x" })
    expect(readings.dumpable).toBe(1)
    expect(readings.fresh).toBe(1)
    expect(readings.never).toBe(0)
  })

  test("an empty fleet reads as zeros", () => {
    expect(fleetReadings(undefined).total).toBe(0)
  })
})

describe("the samples a tile keeps", () => {
  test("one per reading, none for a reading already held, never more than asked", () => {
    let samples = pushSample([], "t1", 3)
    samples = pushSample(samples, "t1", 9)
    expect(samples).toEqual([{ at: "t1", value: 3 }])
    samples = pushSample(samples, "t2", 4, 2)
    samples = pushSample(samples, "t3", 5, 2)
    expect(samples.map((sample) => sample.value)).toEqual([4, 5])
  })
})

describe("the table's order", () => {
  const rows = [
    entry({ id: 1, name: "b", bytes: 10, sessions: 1 }),
    entry({ id: 2, name: "a", bytes: 30, sessions: 1 }),
    entry({ id: 3, name: "c", bytes: 0, sizesKnown: false, sessions: 7 }),
  ]
  const names = (sort) => sortRows(rows, sort, (one) => one.lastBackup).map((one) => one.name)

  test("unsorted is the order the rows came in", () => {
    expect(sortRows(rows, null, () => undefined)).toBe(rows)
  })

  test("by a column, either way, with the name settling ties and an unknown size last", () => {
    expect(names({ key: "name", dir: "asc" })).toEqual(["a", "b", "c"])
    expect(names({ key: "size", dir: "desc" })).toEqual(["a", "b", "c"])
    expect(names({ key: "sessions", dir: "desc" })).toEqual(["c", "a", "b"])
  })

  test("a heading goes natural, reversed, then off", () => {
    expect(nextSort(null, "size")).toEqual({ key: "size", dir: "desc" })
    expect(nextSort({ key: "size", dir: "desc" }, "size")).toEqual({ key: "size", dir: "asc" })
    expect(nextSort({ key: "size", dir: "asc" }, "size")).toBeNull()
    expect(nextSort({ key: "size", dir: "asc" }, "name")).toEqual({ key: "name", dir: "asc" })
  })
})

describe("the map", () => {
  const topology = {
    checkedAt: "2026-09-25T12:00:00Z",
    nodes: [
      { id: "db:1", kind: "database", name: "shop", product: "postgres", connId: 1 },
      { id: "db:2", kind: "database", name: "cache", product: "redis", connId: 2 },
      {
        id: "deploy:7",
        kind: "deployment",
        name: "storefront",
        product: "nodejs",
        status: "running",
      },
      { id: "container:worker", kind: "container", name: "worker", status: "running" },
      { id: "host", kind: "host", name: "This server" },
    ],
    edges: [
      { from: "db:1", to: "deploy:7", via: ["binding"], sessions: 0, status: "broken" },
      { from: "db:2", to: "deploy:7", via: ["env"], sessions: 3, status: "connected" },
      { from: "db:1", to: "host", via: ["session"], sessions: 2, status: "observed" },
      {
        from: "db:2",
        to: "container:worker",
        via: ["stack", "network"],
        sessions: 0,
        status: "observed",
      },
    ],
  }

  test("databases on one side, everything else on the other, broken links first", () => {
    const { databases, consumers, feeds, fedBy } = splitTopology(topology)
    expect(databases.map((n) => n.id)).toEqual(["db:1", "db:2"])
    expect(consumers.map((n) => n.id)).toEqual(["deploy:7", "host", "container:worker"])
    expect(feeds.get("db:1")).toHaveLength(2)
    expect(fedBy.get("deploy:7").map((f) => f.from.name)).toEqual(["shop", "cache"])
  })

  test("narrowed to some databases, the others leave with their links and their readers", () => {
    const split = splitTopology(topology, (node) => node.connId === 2)
    expect(split.databases.map((n) => n.id)).toEqual(["db:2"])
    expect(split.consumers.map((n) => n.id)).toEqual(["deploy:7", "container:worker"])
    expect(split.edges).toHaveLength(2)
    expect(topologyReadings(split)).toEqual({
      databases: 1,
      consumers: 2,
      links: 2,
      sessions: 3,
      wrong: 0,
    })
  })

  test("the map's figures count readers that are fed, sessions carried and links that are wrong", () => {
    expect(topologyReadings(splitTopology(topology))).toEqual({
      databases: 2,
      consumers: 3,
      links: 4,
      sessions: 5,
      wrong: 1,
    })
  })

  test("what each database feeds, with the products for its marks", () => {
    const feeds = feedsByConnection(topology)
    expect(feeds.get(1)).toEqual({ products: ["nodejs"], count: 2, sessions: 2 })
    expect(feeds.get(2)).toEqual({ products: ["nodejs"], count: 2, sessions: 3 })
    expect(feedsByConnection(undefined).size).toBe(0)
  })

  test("a link is described by how it is known", () => {
    expect(describeEdge(topology.edges[1])).toBe("named in its environment · 3 open sessions")
    expect(describeEdge(topology.edges[3])).toBe("same compose stack · shares a network")
    expect(shortEdge(topology.edges[1])).toBe("3 open")
    expect(shortEdge(topology.edges[0])).toBe("linked")
    expect(worstStatus(topology.edges)).toBe("broken")
    expect(worstStatus([])).toBe("observed")
  })

  test("nothing yet is two empty lanes", () => {
    const { databases, consumers } = splitTopology(undefined)
    expect(databases).toEqual([])
    expect(consumers).toEqual([])
  })
})
