import { describe, expect, test } from "bun:test"
import { databaseCensus, databaseCensusLine, detectDatabaseSystems } from "./database-systems"
import { boardLayout, composeLayout, decodeDocument, DEFAULT_DOCUMENT } from "./server-map"
import { detectSystems } from "./server-systems"

const item = (id, name, extra = {}) => ({
  id,
  name,
  tone: "ok",
  state: "running",
  href: `/x/${encodeURIComponent(id)}`,
  ...extra,
})
const reading = (key, items, extra = {}) => ({
  key,
  total: items.length,
  items,
  state: "read",
  ...extra,
})
const link = (from, to, via, also) => ({ from, to, via, ...(also ? { also } : {}) })

/**
 * A shop deployment bound to a Postgres served by its own container, which
 * keeps its data in a volume a nightly job backs up and is dumped itself; a
 * Redis the same deployment names in its environment; and an archive nothing
 * reads — with a domain, a key and an image the databases map leaves out.
 */
function serverMap() {
  return {
    host: { hostname: "box" },
    checkedAt: "2026-11-10T00:00:00Z",
    kinds: [
      reading("projects", [item("project:7", "storefront", { product: "nextjs", projectId: 7 })]),
      reading("containers", [
        item("container:c-pg", "shop-db", { product: "postgres" }),
        item("container:c-redis", "cache", { product: "redis" }),
        item("container:c-worker", "worker", { product: "python" }),
      ]),
      reading("databases", [
        item("db:1", "shop", { product: "postgres", state: "answers" }),
        item("db:4", "cache", { product: "redis", state: "answers" }),
        item("db:9", "archive", { product: "postgres", state: "answers" }),
      ]),
      reading("volumes", [item("volume:pg", "shop-pg-data")]),
      reading("backups", [item("backup:1", "nightly"), item("backup:2", "volumes")]),
      reading("domains", [item("domain:shop.example.com", "shop.example.com")]),
      reading("images", [item("image:sha", "postgres:16")]),
    ],
    links: [
      link("project:7", "db:1", "binding"),
      link("container:c-pg", "db:1", "serves"),
      link("container:c-pg", "volume:pg", "mount"),
      link("volume:pg", "backup:2", "backup"),
      link("db:1", "backup:1", "backup"),
      link("container:c-redis", "db:4", "serves"),
      link("project:7", "db:4", "env"),
      link("project:7", "domain:shop.example.com", "route"),
    ],
  }
}

/** What the databases topology adds: sessions, a broken link, this server, another machine. */
function topology() {
  return {
    checkedAt: "2026-11-10T00:00:00Z",
    nodes: [
      { id: "db:1", kind: "database", name: "shop", product: "postgres", connId: 1 },
      { id: "db:4", kind: "database", name: "cache", product: "redis", connId: 4 },
      { id: "db:9", kind: "database", name: "archive", product: "postgres", connId: 9 },
      { id: "db:12", kind: "database", name: "notes", product: "sqlite", connId: 12 },
      {
        id: "deploy:3",
        kind: "deployment",
        name: "storefront",
        projectId: 7,
        status: "running",
      },
      { id: "container:worker", kind: "container", name: "worker", status: "running" },
      { id: "container:loose", kind: "container", name: "loose", status: "exited" },
      { id: "host", kind: "host", name: "This server", detail: "processes on the machine" },
      { id: "remote:10.0.0.9", kind: "remote", name: "10.0.0.9", detail: "another machine" },
    ],
    edges: [
      { from: "db:1", to: "deploy:3", via: ["binding", "session"], sessions: 3, status: "broken" },
      { from: "db:1", to: "container:worker", via: ["stack", "network"], sessions: 0, status: "" },
      { from: "db:1", to: "host", via: ["session"], sessions: 1, status: "observed" },
      { from: "db:4", to: "host", via: ["session"], sessions: 2, status: "observed" },
      { from: "db:4", to: "remote:10.0.0.9", via: ["session"], sessions: 6, status: "observed" },
      { from: "db:12", to: "container:loose", via: ["env"], sessions: 0, status: "observed" },
    ],
  }
}

const system = (systems, id) => systems.find((s) => s.id === id)
const node = (s, itemId) => s.nodes.find((n) => n.itemId === itemId)
const edge = (s, a, b) =>
  s.edges.find(
    (e) =>
      (e.source.endsWith(`/${a}`) && e.target.endsWith(`/${b}`)) ||
      (e.source.endsWith(`/${b}`) && e.target.endsWith(`/${a}`)),
  )

describe("a database's diagram", () => {
  test("is one for each database, and holds nothing a database is not wired to", () => {
    const systems = detectDatabaseSystems(serverMap(), undefined)
    expect(systems.map((s) => s.id).sort()).toEqual(["system:db:1", "system:db:4", "system:db:9"])
    expect(systems.every((s) => s.kind === "database")).toBe(true)
    const shop = system(systems, "system:db:1")
    expect(shop.nodes.map((n) => n.itemId).sort()).toEqual([
      "backup:1",
      "backup:2",
      "container:c-pg",
      "db:1",
      "project:7",
      "volume:pg",
    ])
    // A domain the deployment answers to is the server map's to draw, not this one's.
    expect(node(shop, "domain:shop.example.com")).toBeUndefined()
    expect(system(systems, "system:db:9").nodes.map((n) => n.itemId)).toEqual(["db:9"])
  })

  test("runs from what reads it, through the database, to where it runs, keeps and is copied", () => {
    const shop = system(detectDatabaseSystems(serverMap(), undefined), "system:db:1")
    const x = (id) => node(shop, id).x
    expect(x("project:7")).toBeLessThan(x("db:1"))
    expect(x("db:1")).toBeLessThan(x("container:c-pg"))
    expect(x("container:c-pg")).toBeLessThan(x("volume:pg"))
    expect(x("volume:pg")).toBeLessThan(x("backup:2"))
    // Every line runs left to right.
    for (const e of shop.edges) {
      const s = shop.nodes.find((n) => n.id === e.source)
      const t = shop.nodes.find((n) => n.id === e.target)
      expect(s.x).toBeLessThanOrEqual(t.x)
    }
    expect(node(shop, "db:1").face).toBe("datastore")
    expect(node(shop, "project:7").face).toBe("hero")
  })

  test("what two databases are wired to is drawn in each and marked shared", () => {
    const systems = detectDatabaseSystems(serverMap(), topology())
    const inShop = node(system(systems, "system:db:1"), "project:7")
    expect(inShop.shared).toEqual(["system:db:4"])
    expect(node(system(systems, "system:db:4"), "host").shared).toEqual(["system:db:1"])
  })

  test("does not change how the server map lays its own database systems out", () => {
    const before = JSON.stringify(detectSystems(serverMap()))
    detectDatabaseSystems(serverMap(), topology())
    expect(JSON.stringify(detectSystems(serverMap()))).toBe(before)
  })
})

describe("joined with the topology", () => {
  test("adds this server and another machine, each with the sessions it holds", () => {
    const systems = detectDatabaseSystems(serverMap(), topology())
    const cache = system(systems, "system:db:4")
    const host = node(cache, "host")
    expect(host.kind).toBe("host")
    expect(host.face).toBe("process")
    // The box says what it holds across every database; each line its own.
    expect(host.item.state).toBe("3 sessions open")
    expect(edge(cache, "host", "db:4").sessions).toBe(2)
    expect(node(cache, "remote:10.0.0.9").item.href).toBe("/network/connections?q=10.0.0.9")
  })

  test("names a deployment's link as the surest way it is known, and says it broke", () => {
    const shop = system(detectDatabaseSystems(serverMap(), topology()), "system:db:1")
    const line = edge(shop, "project:7", "db:1")
    expect(line.via).toBe("binding")
    expect(line.also).toEqual(["session"])
    expect(line.tone).toBe("danger")
    expect(line.sessions).toBe(3)
    // The deployment is the server map's project, not a second box for the environment.
    expect(node(shop, "deploy:3")).toBeUndefined()
  })

  test("a container in the same stack is wired by how it could reach it", () => {
    const shop = system(detectDatabaseSystems(serverMap(), topology()), "system:db:1")
    const line = edge(shop, "container:c-worker", "db:1")
    expect(line.via).toBe("network")
    expect(line.also).toEqual(["stack"])
  })

  test("a database or a reader the map has not read is drawn from the topology alone", () => {
    const systems = detectDatabaseSystems(undefined, topology())
    const notes = system(systems, "system:db:12")
    expect(node(notes, "db:12").item).toMatchObject({ tone: "unknown", state: "not read yet" })
    expect(node(notes, "container:loose").item).toMatchObject({
      tone: "danger",
      state: "exited",
    })
    // The deployment stands in for its project until the map names it.
    const shop = system(systems, "system:db:1")
    expect(node(shop, "project:7").item.name).toBe("storefront")
  })

  test("nothing read is nothing drawn", () => {
    expect(detectDatabaseSystems(undefined, undefined)).toEqual([])
  })
})

describe("what a diagram says of itself", () => {
  test("what reads it, the sessions open and where it runs", () => {
    const systems = detectDatabaseSystems(serverMap(), topology())
    const shop = system(systems, "system:db:1")
    const c = databaseCensus(shop)
    expect(c.readers.map((n) => n.itemId).sort()).toEqual([
      "container:c-worker",
      "host",
      "project:7",
    ])
    expect(c.runs.map((n) => n.itemId)).toEqual(["container:c-pg"])
    expect(c.sessions).toBe(4)
    expect(c.volumes).toBe(1)
    expect(c.backups).toBe(2)
    expect(databaseCensusLine(shop)).toBe("feeds 3 things · 4 sessions open · in shop-db")
    expect(databaseCensusLine(shop, 2)).toBe("feeds 3 things · 4 sessions open")
    expect(databaseCensusLine(system(systems, "system:db:9"))).toBe("nothing seen reading it")
  })
})

describe("the arrangement of each board", () => {
  const server = { ...DEFAULT_DOCUMENT, positions: { "system:project:1/db:1": { x: 1, y: 2 } } }
  const databases = { ...DEFAULT_DOCUMENT, frames: { "system:db:1": { x: 5, y: 6 } } }

  test("the server map's is the document's top, the databases map's under boards", () => {
    const whole = composeLayout(composeLayout(null, undefined, server), "databases", databases)
    expect(decodeDocument(boardLayout(whole))).toEqual(server)
    expect(decodeDocument(boardLayout(whole, "databases"))).toEqual(databases)
  })

  test("saving one keeps the other", () => {
    const whole = composeLayout(null, "databases", databases)
    const moved = { ...server, focus: "system:project:1" }
    const after = composeLayout(whole, undefined, moved)
    expect(decodeDocument(boardLayout(after, "databases"))).toEqual(databases)
    expect(decodeDocument(boardLayout(after)).focus).toBe("system:project:1")
    const again = composeLayout(after, "databases", DEFAULT_DOCUMENT)
    expect(decodeDocument(boardLayout(again)).focus).toBe("system:project:1")
  })

  test("a board nothing was kept for is laid out fresh", () => {
    expect(decodeDocument(boardLayout(null, "databases"))).toEqual(DEFAULT_DOCUMENT)
    expect(decodeDocument(boardLayout({ version: 2, positions: {} }, "databases"))).toEqual(
      DEFAULT_DOCUMENT,
    )
  })
})
