import { describe, expect, test } from "bun:test"
import {
  CAPABILITY_FLAGS,
  SECTION_IDS,
  engineFor,
  engineOf,
  kindWord,
  sectionGroups,
  sectionHref,
  sectionsFor,
} from "./engine"

const DRIVERS = [
  "postgres",
  "mysql",
  "sqlite",
  "sqlserver",
  "clickhouse",
  "oracle",
  "mongodb",
  "redis",
]

/** Every flavour the backend can report, under the driver that talks to it. */
const FLAVORS = {
  postgres: ["postgres", "timescaledb", "cockroachdb", "yugabytedb"],
  mysql: ["mysql", "mariadb", "percona", "tidb"],
  redis: ["redis", "valkey", "keydb", "dragonfly"],
  mongodb: ["mongodb", "ferretdb"],
  sqlserver: ["sqlserver", "azure-sql-edge"],
  sqlite: ["sqlite"],
  clickhouse: ["clickhouse"],
  oracle: ["oracle"],
}

const ids = (engine) => sectionsFor(engine).map((section) => section.id)
const titles = (engine) => sectionsFor(engine).map((section) => section.title)

describe("what an engine is", () => {
  test("every driver is named, drawn, and given its own words", () => {
    for (const driver of DRIVERS) {
      const engine = engineOf(driver)
      expect(engine.driver).toBe(driver)
      expect(engine.id).toBe(driver)
      expect(engine.label).not.toBe("")
      expect(engine.logo).toBe(driver)
      expect(engine.cli).not.toBe("")
      expect(engine.dsnExample).not.toBe("")
      expect(Object.keys(engine.capabilities).sort()).toEqual([...CAPABILITY_FLAGS].sort())
    }
    expect(engineOf("postgres").defaultPort).toBe("5432")
    expect(engineOf("sqlite").defaultPort).toBe("")
  })

  test("every flavour resolves under its driver with a name of its own", () => {
    for (const [driver, flavors] of Object.entries(FLAVORS)) {
      for (const flavor of flavors) {
        const engine = engineFor({ driver, flavor })
        expect(engine.driver).toBe(driver)
        expect(engine.id).toBe(flavor)
        expect(engine.kind).toBe(engineOf(driver).kind)
      }
    }
    expect(engineFor({ driver: "mysql", flavor: "mariadb" }).label).toBe("MariaDB")
    expect(engineFor({ driver: "redis", flavor: "valkey" }).label).toBe("Valkey")
  })

  test("a flavour is drawn as itself where it has artwork and as its driver where it has none", () => {
    expect(engineFor({ driver: "mysql", flavor: "mariadb" }).logo).toBe("mariadb")
    expect(engineFor({ driver: "redis", flavor: "valkey" }).logo).toBe("valkey")
    expect(engineFor({ driver: "redis", flavor: "keydb" }).logo).toBe("redis")
    expect(engineFor({ driver: "postgres", flavor: "cockroachdb" }).logo).toBe("postgres")
  })

  test("the command-line client follows the flavour", () => {
    expect(engineFor({ driver: "mysql", flavor: "mariadb" }).cli).toBe("mariadb")
    expect(engineFor({ driver: "redis", flavor: "valkey" }).cli).toBe("valkey-cli")
    expect(engineFor({ driver: "redis", flavor: "dragonfly" }).cli).toBe("redis-cli")
  })

  test("a flavour that is not the driver's own is not believed", () => {
    const engine = engineFor({ driver: "postgres", flavor: "mariadb" })
    expect(engine.id).toBe("postgres")
    expect(engine.label).toBe("PostgreSQL")
  })

  test("the kinds and the words each kind uses", () => {
    expect(engineOf("postgres").kind).toBe("sql")
    expect(engineOf("mongodb").kind).toBe("document")
    expect(engineOf("redis").kind).toBe("keyvalue")
    expect(kindWord("keyvalue")).toBe("key–value")
    expect(engineOf("postgres").nouns).toMatchObject({
      object: "table",
      container: "schema",
      row: "row",
    })
    expect(engineOf("mysql").nouns.container).toBe("database")
    expect(engineOf("mongodb").nouns).toMatchObject({ object: "collection", row: "document" })
    expect(engineOf("redis").nouns).toMatchObject({ object: "key", objects: "keys" })
  })

  test("the names an image or a provisioning option uses reach the same engine", () => {
    expect(engineOf("mongo").driver).toBe("mongodb")
    expect(engineOf("postgresql").driver).toBe("postgres")
    expect(engineOf("pgvector").kind).toBe("sql")
    expect(engineOf("MariaDB").id).toBe("mariadb")
  })

  test("an engine with no driver is named and has no pages", () => {
    const memcached = engineOf("memcached")
    expect(memcached).toMatchObject({ driver: "", label: "Memcached", kind: "cache" })
    expect(memcached.sections).toEqual([])
    expect(engineOf("neo4j").kindWord).toBe("graph")
    expect(engineOf("qdrant").logo).toBe("qdrant")
    // Nothing bundled is never a guess.
    expect(engineOf("memcached").logo).toBeUndefined()
    expect(engineOf("something-new")).toMatchObject({ driver: "", label: "something-new" })
    expect(engineOf("something-new").logo).toBeUndefined()
  })
})

describe("which pages an engine has", () => {
  test("a SQL engine has every page", () => {
    expect(ids(engineOf("postgres"))).toEqual([...SECTION_IDS])
  })

  test("Redis has keys and a console, and no SQL page", () => {
    const redis = engineOf("redis")
    expect(ids(redis)).toEqual([
      "home",
      "data",
      "query",
      "performance",
      "logs",
      "access",
      "backups",
      "settings",
    ])
    expect(titles(redis).slice(1, 3)).toEqual(["Keys", "Console"])
    for (const section of ["schema", "diagram", "search", "generate"])
      expect(redis.has(section)).toBe(false)
  })

  test("MongoDB has documents, aggregations and a schema", () => {
    const mongo = engineOf("mongodb")
    expect(ids(mongo)).toEqual([
      "home",
      "data",
      "query",
      "schema",
      "performance",
      "logs",
      "access",
      "backups",
      "settings",
    ])
    expect(titles(mongo).slice(1, 3)).toEqual(["Documents", "Aggregations"])
  })

  test("a page a capability denies is gone, with the reason its address shows", () => {
    const sqlite = engineOf("sqlite")
    expect(sqlite.has("access")).toBe(false)
    expect(sqlite.missing("access")).toMatchObject({ thing: "roles" })
    expect(sqlite.missing("access").reason).toContain("file")
    expect(engineOf("redis").missing("schema")).toEqual({
      thing: "schema",
      reason: "Redis is a key–value store. Its keys are under Keys.",
    })
  })

  test("the rail's groups hold only what the engine has", () => {
    expect(sectionGroups(engineOf("postgres")).map((g) => g.label)).toEqual([
      undefined,
      "Work",
      "Schema",
      "Insights",
      "Operate",
    ])
    const redis = sectionGroups(engineOf("redis"))
    expect(redis.map((g) => g.label)).toEqual([undefined, "Work", "Insights", "Operate"])
    expect(redis[1].sections.map((s) => s.title)).toEqual(["Keys", "Console"])
  })
})

describe("the server's word wins", () => {
  const catalogue = (overrides) => [
    {
      id: "redis",
      label: "Redis",
      kind: "keyvalue",
      placeholder: "redis://…",
      sql: false,
      ddl: false,
      ...overrides,
    },
  ]

  test("without a catalogue the registry's own table answers", () => {
    expect(engineOf("redis").can("advisor")).toBe(false)
    expect(engineOf("postgres").can("locks")).toBe(false)
    expect(engineOf("postgres").can("roles")).toBe(true)
    expect(engineOf("postgres").can("no-such-flag")).toBe(false)
  })

  test("today's catalogue decides sql and ddl", () => {
    const drivers = [
      { id: "clickhouse", label: "ClickHouse", kind: "sql", placeholder: "", sql: true, ddl: true },
    ]
    expect(engineOf("clickhouse").can("ddl")).toBe(false)
    expect(engineOf("clickhouse", drivers).can("ddl")).toBe(true)
  })

  test("a driver's capabilities add the pages they open", () => {
    const drivers = catalogue({ capabilities: { advisor: true, dump: false } })
    const redis = engineFor({ driver: "redis" }, drivers)
    expect(redis.has("advisor")).toBe(true)
    expect(redis.has("backups")).toBe(false)
    // A flag the server did not mention keeps the registry's answer.
    expect(redis.can("roles")).toBe(true)
  })

  test("a flavour's capabilities win over its driver's, and the connection's over both", () => {
    const drivers = catalogue({
      capabilities: { settings: true },
      flavors: [
        { id: "redis", label: "Redis", capabilities: { settings: true } },
        { id: "keydb", label: "KeyDB", capabilities: { settings: false, locks: "sampled" } },
      ],
      defaultPort: 6380,
      dsnExample: "redis://example",
    })
    const keydb = engineFor({ driver: "redis", flavor: "keydb" }, drivers)
    expect(keydb.can("settings")).toBe(false)
    expect(keydb.can("locks")).toBe(true)
    expect(keydb.defaultPort).toBe("6380")
    expect(keydb.dsnExample).toBe("redis://example")
    const own = engineFor(
      { driver: "redis", flavor: "keydb", capabilities: { settings: true } },
      drivers,
    )
    expect(own.can("settings")).toBe(true)
  })
})

describe("addresses", () => {
  test("home is the bare path and every other page hangs off it", () => {
    expect(sectionHref(7)).toBe("/databases/7")
    expect(sectionHref(7, "home")).toBe("/databases/7")
    expect(sectionHref(7, "data")).toBe("/databases/7/data")
  })

  test("the selection is written in one order and empty values are left out", () => {
    expect(sectionHref(7, "data", { table: "orders", schema: "public" })).toBe(
      "/databases/7/data?schema=public&table=orders",
    )
    expect(sectionHref(7, "data", { schema: "", table: null, key: undefined })).toBe(
      "/databases/7/data",
    )
    expect(sectionHref(7, "query", { sql: "SELECT 1;" })).toBe("/databases/7/query?sql=SELECT+1%3B")
    expect(sectionHref(7, "logs", { view: "queries", source: "docker:a" })).toBe(
      "/databases/7/logs?view=queries&source=docker%3Aa",
    )
  })

  test("a link keeps the part of the place the page it opens can use", () => {
    const postgres = engineOf("postgres")
    expect(postgres.section("schema").carries).toEqual(["schema", "table"])
    expect(postgres.section("query").carries).toEqual(["schema"])
    expect(postgres.section("settings").carries).toEqual([])
    expect(engineOf("redis").section("data").carries).toEqual(["db"])
    expect(engineOf("mongodb").section("data").carries).toEqual(["db", "collection"])
  })
})

describe("how a program connects", () => {
  const parts = {
    url: "mysql://app:pw@10.0.0.5:3306/shop",
    host: "10.0.0.5",
    port: "3306",
    user: "app",
    database: "shop",
  }

  test("the Go driver's own string becomes the URL applications expect", () => {
    expect(engineOf("mysql").url("app:pw@tcp(10.0.0.5:3306)/shop")).toBe(parts.url)
    expect(engineOf("postgres").url("postgres://a@b/c")).toBe("postgres://a@b/c")
  })

  test("a shell that takes fields asks for the password itself", () => {
    expect(engineOf("mysql").command(parts)).toBe(
      "mysql --host=10.0.0.5 --port=3306 --user=app --password shop",
    )
    expect(engineOf("mariadb").command(parts)).toBe(
      "mariadb --host=10.0.0.5 --port=3306 --user=app --password shop",
    )
    expect(engineOf("redis").command({ ...parts, url: "redis://h/0" })).toBe(
      'redis-cli -u "redis://h/0"',
    )
    expect(engineOf("valkey").command({ ...parts, url: "redis://h/0" })).toBe(
      'valkey-cli -u "redis://h/0"',
    )
    expect(engineOf("sqlite").command({ ...parts, database: "/data/app.db" })).toBe(
      'sqlite3 "/data/app.db"',
    )
  })

  test("every engine has at least one client snippet that names the address", () => {
    for (const driver of DRIVERS) {
      const engine = engineOf(driver)
      expect(engine.clients.length).toBeGreaterThan(0)
      for (const client of engine.clients) {
        const code = client.code({ ...parts, url: "URL", database: "DATABASE" })
        expect(code.includes("URL") || code.includes("DATABASE")).toBe(true)
      }
    }
  })
})
