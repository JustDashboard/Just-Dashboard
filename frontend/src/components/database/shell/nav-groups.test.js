import { describe, expect, test } from "bun:test"
import { engineFor } from "../engine"
import { databaseNavGroups, knownDatabase, knownDatabases, learnedDatabase } from "./nav-groups"

const titles = (engine) =>
  databaseNavGroups(engine.sections, (section) => `/databases/7/${section}`).map((group) => [
    group.label,
    group.items.map((item) => item.title),
  ])

describe("a database's pages as the rail's panel", () => {
  test("Home alone, then the four groups, in the engine's own words", () => {
    expect(titles(engineFor({ driver: "redis" }))).toEqual([
      [undefined, ["Home"]],
      ["Work", ["Keys", "Console"]],
      ["Insights", ["Performance", "Logs"]],
      ["Operate", ["Access", "Backups", "Settings"]],
    ])
    expect(titles(engineFor({ driver: "mongodb" }))[1]).toEqual([
      "Work",
      ["Documents", "Aggregations"],
    ])
  })

  test("a group the engine has nothing in is left out", () => {
    expect(titles(engineFor({ driver: "redis" })).map(([label]) => label)).not.toContain("Schema")
  })
})

describe("what the rail remembers of the databases it has drawn", () => {
  const list = [
    { id: 1, name: "shop", driver: "postgres" },
    { id: 2, name: "blog", driver: "mysql" },
  ]

  test("every listed connection and none that is gone", () => {
    const held = knownDatabases(list, { 9: { name: "old", driver: "redis" } })
    expect(held).toEqual({
      1: { name: "shop", driver: "postgres" },
      2: { name: "blog", driver: "mysql" },
    })
  })

  test("a list that says the same thing writes nothing", () => {
    const held = knownDatabases(list, {})
    expect(knownDatabases(list, held)).toBe(held)
  })

  test("what the server said it is survives the next read of the list", () => {
    const first = knownDatabases(list, {})
    const learned = learnedDatabase(first, 2, {
      flavor: "mariadb",
      capabilities: { dump: false },
    })
    expect(learned[2]).toEqual({
      name: "blog",
      driver: "mysql",
      flavor: "mariadb",
      capabilities: { dump: false },
    })
    expect(knownDatabases(list, learned)).toBe(learned)
    // The panel drawn from it is the product's, without its dumps.
    const pages = engineFor(learned[2]).sections.map((section) => section.id)
    expect(engineFor(learned[2]).label).toBe("MariaDB")
    expect(pages).not.toContain("backups")
  })

  test("a row pointed at another engine forgets what the old one was", () => {
    const learned = learnedDatabase(knownDatabases(list, {}), 2, { flavor: "mariadb" })
    const moved = knownDatabases([list[0], { id: 2, name: "blog", driver: "postgres" }], learned)
    expect(moved[2]).toEqual({ name: "blog", driver: "postgres" })
  })

  test("the same answer again, or one for a row the list never had, changes nothing", () => {
    const held = learnedDatabase(knownDatabases(list, {}), 2, { flavor: "mariadb" })
    expect(learnedDatabase(held, 2, { flavor: "mariadb" })).toBe(held)
    expect(learnedDatabase(held, 99, { flavor: "valkey" })).toBe(held)
  })

  test("an id is looked up as an entry, not as something every object has", () => {
    expect(knownDatabase({}, "constructor")).toBeUndefined()
    expect(knownDatabase({ 4: { name: "cache", driver: "redis" } }, 4)?.name).toBe("cache")
  })
})
