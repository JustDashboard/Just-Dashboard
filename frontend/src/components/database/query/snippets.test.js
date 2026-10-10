import { describe, expect, test } from "bun:test"
import { dialectOf } from "./dialect"
import { SNIPPET_ENGINES, SNIPPET_GROUPS, snippetsFor } from "./snippets"
import { splitStatements } from "./sql-text"

/** The words the server reads as a write wherever they stand in a statement's text. */
const WRITES =
  /\b(insert|update|delete|drop|truncate|alter|create|grant|revoke|merge|replace|refresh|copy|exec|execute|set|declare|begin|call|into)\b/i

describe("the diagnostics", () => {
  test("the six engines of the brief each have sizes, activity, indexes and health", () => {
    for (const engine of ["postgres", "mysql", "mariadb", "sqlite", "sqlserver", "clickhouse"]) {
      const groups = new Set(snippetsFor(engine, engine).map((snippet) => snippet.group))
      for (const group of ["Size", "Indexes", "Health"]) expect(groups.has(group)).toBe(true)
      if (engine !== "sqlite") expect(groups.has("Activity")).toBe(true)
    }
  })
  test("MariaDB answers as itself, and a flavour with no list of its own as its driver", () => {
    const maria = snippetsFor("mariadb", "mysql")
      .map((snippet) => snippet.sql)
      .join("\n")
    const mysql = snippetsFor("mysql", "mysql")
      .map((snippet) => snippet.sql)
      .join("\n")
    expect(maria).toContain("information_schema.innodb_lock_waits")
    expect(mysql).toContain("sys.innodb_lock_waits")
    expect(snippetsFor("percona", "mysql")).toBe(snippetsFor("mysql", "mysql"))
    expect(snippetsFor("timescaledb", "postgres")).toBe(snippetsFor("postgres", "postgres"))
    expect(snippetsFor("redis", "redis")).toEqual([])
    expect(snippetsFor("constructor", "toString")).toEqual([])
  })
  for (const engine of SNIPPET_ENGINES) {
    const driver = engine === "mariadb" ? "mysql" : engine
    const list = snippetsFor(engine, driver)
    test(`${engine}: every one is a single statement that only reads, named once`, () => {
      expect(list.length).toBeGreaterThanOrEqual(8)
      expect(new Set(list.map((snippet) => snippet.id)).size).toBe(list.length)
      expect(new Set(list.map((snippet) => snippet.title)).size).toBe(list.length)
      for (const snippet of list) {
        expect(SNIPPET_GROUPS).toContain(snippet.group)
        expect(snippet.about.length).toBeGreaterThan(10)
        expect(splitStatements(snippet.sql, dialectOf(driver))).toHaveLength(1)
        expect(snippet.sql.trimStart()).toMatch(/^select\b/i)
        // The server finds a write's verb anywhere in the text, a column's name included.
        expect(WRITES.exec(snippet.sql)?.[0]).toBeUndefined()
      }
    })
  }
})
