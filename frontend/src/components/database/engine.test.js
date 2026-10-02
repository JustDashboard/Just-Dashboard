import { describe, expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import path from "node:path"
import {
  CAPABILITY_NAMES,
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

/**
 * What `GET /databases/drivers` serves. The file is the backend's own record
 * of the route, held to it by `TestTheDriverCatalogueSnapshotIsCurrent`, so
 * what is asserted here about the server's table is asserted about the real
 * one.
 */
const CATALOGUE = JSON.parse(
  readFileSync(
    path.join(import.meta.dir, "../../../../backend/internal/api/testdata/database-drivers.json"),
    "utf8",
  ),
)

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
      expect(Object.keys(engine.capabilities).sort()).toEqual([...CAPABILITY_NAMES].sort())
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

  test("a flavour is drawn as itself where it has artwork, and never as another product", () => {
    for (const [driver, flavor] of [
      ["mysql", "mariadb"],
      ["mysql", "tidb"],
      ["redis", "valkey"],
      ["postgres", "cockroachdb"],
      ["postgres", "timescaledb"],
      ["postgres", "yugabytedb"],
      ["mongodb", "ferretdb"],
      // Drawn as the SQL Server engine it is; the id is still its own.
      ["sqlserver", "azure-sql-edge"],
    ]) {
      expect(engineFor({ driver, flavor }).logo).toBe(flavor)
    }
    // No artwork of its own is the kind's glyph, not the driver's product
    // with this one's name beside it.
    for (const [driver, flavor] of [
      ["redis", "keydb"],
      ["redis", "dragonfly"],
      ["mysql", "percona"],
    ]) {
      expect(engineFor({ driver, flavor }).logo).toBeUndefined()
    }
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

  test("a name every object inherits is not an engine", () => {
    for (const name of ["constructor", "toString", "__proto__", "hasOwnProperty"]) {
      expect(engineFor({ driver: "mysql", flavor: name })).toMatchObject({
        id: "mysql",
        label: "MySQL",
        cli: "mysql",
      })
      expect(engineOf(name)).toMatchObject({ id: name.toLowerCase(), driver: "", label: name })
      expect(engineOf(name).sections).toEqual([])
      // Nor a product with artwork: the tile keeps the database glyph.
      expect(engineOf(name).logo).toBeUndefined()
      expect(engineOf("postgres").can(name)).toBe(false)
    }
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
    expect(engineOf("memcached").logo).toBe("memcached")
    // Nothing bundled is never a guess.
    expect(engineOf("something-new")).toMatchObject({ driver: "", label: "something-new" })
    expect(engineOf("something-new").logo).toBeUndefined()
  })
})

describe("which pages an engine has", () => {
  test("a SQL engine has every page", () => {
    expect(ids(engineOf("postgres", CATALOGUE))).toEqual([...SECTION_IDS])
  })

  test("Redis has keys and a console, and no SQL page", () => {
    const redis = engineOf("redis", CATALOGUE)
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
    const mongo = engineOf("mongodb", CATALOGUE)
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
    const sqlite = engineOf("sqlite", CATALOGUE)
    expect(sqlite.has("access")).toBe(false)
    expect(sqlite.missing("access")).toMatchObject({ thing: "roles" })
    expect(sqlite.missing("access").reason).toContain("file")
    expect(engineOf("oracle", CATALOGUE).has("access")).toBe(false)
    expect(engineOf("redis", CATALOGUE).missing("schema")).toEqual({
      thing: "schema",
      reason: "Redis is a key–value store. Its keys are under Keys.",
    })
  })

  test("the rail's groups hold only what the engine has", () => {
    expect(sectionGroups(engineOf("postgres", CATALOGUE)).map((g) => g.label)).toEqual([
      undefined,
      "Work",
      "Schema",
      "Insights",
      "Operate",
    ])
    const redis = sectionGroups(engineOf("redis", CATALOGUE))
    expect(redis.map((g) => g.label)).toEqual([undefined, "Work", "Insights", "Operate"])
    expect(redis[1].sections.map((s) => s.title)).toEqual(["Keys", "Console"])
  })
})

describe("the server's table is the answer", () => {
  test("the registry names every capability the server states, and no other", () => {
    expect(CATALOGUE.map((d) => d.id).sort()).toEqual([...DRIVERS].sort())
    for (const info of CATALOGUE) {
      expect(info.flavors.map((f) => f.id)).toEqual(FLAVORS[info.id])
      for (const said of [info, ...info.flavors]) {
        expect(Object.keys(said.capabilities).sort()).toEqual([...CAPABILITY_NAMES].sort())
      }
    }
  })

  test("with the catalogue in hand an engine can do exactly what the server says", () => {
    for (const info of CATALOGUE) {
      for (const flavor of info.flavors) {
        const engine = engineFor({ driver: info.id, flavor: flavor.id }, CATALOGUE)
        expect(engine.capabilities).toEqual(flavor.capabilities)
        expect(engine.label).toBe(flavor.label)
      }
    }
    // What differs between products of one driver is the server's to say.
    const mariadb = engineFor({ driver: "mysql", flavor: "mariadb" }, CATALOGUE)
    const mysql = engineFor({ driver: "mysql", flavor: "mysql" }, CATALOGUE)
    expect(mariadb.capabilities.catalogGroups).toContain("sequences")
    expect(mysql.capabilities.catalogGroups).not.toContain("sequences")
    expect(mariadb.can("sequences")).toBe(true)
    expect(mysql.can("sequences")).toBe(false)
    const cockroach = engineFor({ driver: "postgres", flavor: "cockroachdb" }, CATALOGUE)
    expect(cockroach.can("statements")).toBe(false)
    expect(engineOf("postgres", CATALOGUE).can("statements")).toBe(true)
  })

  test("a flag is on, a word is given, a list holds something", () => {
    const postgres = engineOf("postgres", CATALOGUE)
    expect(postgres.can("changeSets")).toBe(true)
    expect(postgres.can("rowIdentity")).toBe(true)
    expect(postgres.can("ormTargets")).toBe(true)
    expect(postgres.can("keys")).toBe(false)
    const clickhouse = engineOf("clickhouse", CATALOGUE)
    expect(clickhouse.can("changeSets")).toBe(false)
    expect(clickhouse.capabilities.rowIdentity).toBe("none")
    expect(clickhouse.can("ddl")).toBe(false)
    expect(clickhouse.capabilities.ddlOperations).toContain("alterColumn")
    const redis = engineOf("redis", CATALOGUE)
    expect(redis.can("keys")).toBe(true)
    expect(redis.can("ormTargets")).toBe(false)
    expect(redis.can("exportFormats")).toBe(false)
    expect(redis.capabilities.json).toBe("module")
    expect(engineOf("keydb", CATALOGUE).can("json")).toBe(false)
    expect(postgres.can("no-such-flag")).toBe(false)
  })

  test("what the connection's own summary resolved wins over the catalogue", () => {
    const said = { ...CATALOGUE.find((d) => d.id === "redis").capabilities, dump: false }
    const redis = engineFor({ driver: "redis", flavor: "redis", capabilities: said }, CATALOGUE)
    expect(redis.has("backups")).toBe(false)
    expect(redis.can("keys")).toBe(true)
    // And stands alone where the catalogue could not be read.
    const alone = engineFor({ driver: "redis", flavor: "valkey", capabilities: said })
    expect(alone.capabilities).toEqual(said)
  })

  test("the catalogue's own port and example are the engine's", () => {
    const drivers = CATALOGUE.map((d) =>
      d.id === "redis" ? { ...d, defaultPort: 6380, dsnExample: "redis://example" } : d,
    )
    const keydb = engineFor({ driver: "redis", flavor: "keydb" }, drivers)
    expect(keydb.defaultPort).toBe("6380")
    expect(keydb.dsnExample).toBe("redis://example")
    expect(engineOf("sqlite", CATALOGUE).defaultPort).toBe("")
  })
})

describe("before the catalogue has been read", () => {
  test("an engine has the pages the server will confirm, and no other", () => {
    for (const driver of DRIVERS) {
      expect(ids(engineOf(driver))).toEqual(ids(engineOf(driver, CATALOGUE)))
    }
  })

  test("nothing is on that the server does not vouch for", () => {
    for (const driver of DRIVERS) {
      const early = engineOf(driver)
      const said = engineOf(driver, CATALOGUE)
      for (const name of CAPABILITY_NAMES) {
        if (early.can(name)) expect(said.can(name)).toBe(true)
      }
    }
    // The pages are drawn; what is on them waits for the server's word.
    expect(engineOf("postgres").can("roles")).toBe(true)
    expect(engineOf("postgres").can("locks")).toBe(false)
    expect(engineOf("postgres").can("changeSets")).toBe(false)
    expect(engineOf("redis").can("keys")).toBe(false)
    expect(engineOf("postgres").capabilities.ddlOperations).toEqual([])
  })

  test("a catalogue that lacks the driver is no catalogue for it", () => {
    const others = CATALOGUE.filter((d) => d.id !== "redis")
    expect(ids(engineOf("redis", others))).toEqual(ids(engineOf("redis")))
    expect(engineOf("redis", others).can("keys")).toBe(false)
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
    expect(engineOf("mysql").url("app@tcp(10.0.0.5:3306)/shop")).toBe(
      "mysql://app@10.0.0.5:3306/shop",
    )
    expect(engineOf("postgres").url("postgres://a@b/c")).toBe("postgres://a@b/c")
  })

  test("a password the Go driver takes as typed is encoded for the URL", () => {
    expect(engineOf("mysql").url("ap p:p@ss:w/1#@tcp(10.0.0.5:3306)/shop?parseTime=true")).toBe(
      "mysql://ap%20p:p%40ss%3Aw%2F1%23@10.0.0.5:3306/shop?parseTime=true",
    )
  })

  test("a shell that takes fields asks for the password itself", () => {
    expect(engineOf("mysql").command(parts)).toBe(
      "mysql --host=10.0.0.5 --port=3306 --user=app --password shop",
    )
    expect(engineOf("mariadb").command(parts)).toBe(
      "mariadb --host=10.0.0.5 --port=3306 --user=app --password shop",
    )
    expect(engineOf("redis").command({ ...parts, url: "redis://h/0" })).toBe(
      "redis-cli -u redis://h/0",
    )
    expect(engineOf("valkey").command({ ...parts, url: "redis://h/0" })).toBe(
      "valkey-cli -u redis://h/0",
    )
    expect(engineOf("sqlite").command({ ...parts, database: "/data/app.db" })).toBe(
      "sqlite3 /data/app.db",
    )
  })

  test("nothing in a pasted command is read by the shell", () => {
    // Between double quotes `$(…)` and backticks would run.
    const url = "postgres://u:pa$(id)ss`x`'q@h:5432/d?sslmode=disable"
    expect(engineOf("postgres").command({ ...parts, url })).toBe(
      "psql 'postgres://u:pa$(id)ss`x`'\\''q@h:5432/d?sslmode=disable'",
    )
    expect(engineOf("sqlite").command({ ...parts, database: "/data/my app; rm.db" })).toBe(
      "sqlite3 '/data/my app; rm.db'",
    )
    expect(engineOf("mysql").command({ ...parts, user: "a b", database: "$(id)" })).toBe(
      "mysql --host=10.0.0.5 --port=3306 --user='a b' --password '$(id)'",
    )
  })

  test("a quote in a password cannot end a snippet's string early", () => {
    const url = 'mysql://u:pa"s\\s@h:3306/d'
    const node = engineOf("mysql").clients.find((client) => client.id === "node")
    expect(node.code({ ...parts, url })).toContain(
      'createConnection("mysql://u:pa\\"s\\\\s@h:3306/d")',
    )
    const lite = engineOf("sqlite").clients.find((client) => client.id === "python")
    expect(lite.code({ ...parts, database: '/data/a"b.db' })).toContain('connect("/data/a\\"b.db")')
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
