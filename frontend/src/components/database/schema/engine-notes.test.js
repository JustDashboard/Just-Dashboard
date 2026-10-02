import { describe, expect, test } from "bun:test"
import catalogue from "../../../../../backend/internal/api/testdata/database-drivers.json"
import { notesFor, quoteName, quotedRelation } from "./engine-notes"
import { selectStatement } from "./select"

const sql = catalogue.filter((entry) => entry.sql)
const operationsOf = (driver) =>
  catalogue.find((entry) => entry.id === driver).capabilities.ddlOperations

describe("an engine's notes", () => {
  test("every SQL driver of the catalogue is described, not read as plain SQL by default", () => {
    const plain = notesFor({ driver: "nobody" })
    for (const entry of sql) {
      const notes = notesFor({ driver: entry.id })
      // SQLite is plain SQL and says so on purpose; the rest are their own.
      if (entry.id !== "sqlite") expect(notes, entry.id).not.toBe(plain)
    }
  })

  test("an engine whose forms add foreign keys says what one may do, and one without says nothing", () => {
    for (const entry of sql) {
      const notes = notesFor({ driver: entry.id })
      const has = operationsOf(entry.id).includes("foreignKeys")
      expect(notes.onDelete.length > 0, entry.id).toBe(has)
      if (!has) expect(notes.onUpdate, entry.id).toEqual([])
      // NO ACTION is every engine's default: where a clause is offered it is the first choice.
      if (notes.onDelete.length > 0) expect(notes.onDelete[0]).toBe("NO ACTION")
      if (notes.onUpdate.length > 0) expect(notes.onUpdate[0]).toBe("NO ACTION")
    }
  })

  test("an engine whose forms take an index method lists its own, and one without lists none", () => {
    for (const entry of sql) {
      const notes = notesFor({ driver: entry.id })
      expect(notes.indexMethods.length > 0, entry.id).toBe(
        operationsOf(entry.id).includes("indexMethod"),
      )
    }
  })

  test("the limits the contract states are the ones left out", () => {
    expect(notesFor({ driver: "sqlserver" }).onDelete).not.toContain("RESTRICT")
    expect(notesFor({ driver: "sqlserver" }).onUpdate).not.toContain("RESTRICT")
    expect(notesFor({ driver: "oracle" }).onUpdate).toEqual([])
    expect(notesFor({ driver: "oracle" }).onDelete).toEqual(["NO ACTION", "CASCADE", "SET NULL"])
    expect(notesFor({ driver: "mysql" }).onDelete).not.toContain("SET DEFAULT")
    expect(notesFor({ driver: "postgres" }).otherIndexMethods).toBe(true)
    expect(notesFor({ driver: "mysql" }).otherIndexMethods).toBe(false)
  })

  test("a name that is not an own key of the table is an engine nobody described", () => {
    expect(notesFor({ driver: "constructor" }).quote).toEqual(['"', '"'])
    expect(notesFor({ driver: "" }).indexMethods).toEqual([])
  })
})

describe("a quoted name", () => {
  test("is always quoted, with its closing mark doubled inside", () => {
    expect(quoteName("orders", notesFor({ driver: "postgres" }))).toBe('"orders"')
    expect(quoteName('we"ird', notesFor({ driver: "postgres" }))).toBe('"we""ird"')
    expect(quoteName("we`ird", notesFor({ driver: "mysql" }))).toBe("`we``ird`")
    expect(quoteName("we]ird[", notesFor({ driver: "sqlserver" }))).toBe("[we]]ird[]")
  })

  test("carries its schema, or stands alone where there is none to say", () => {
    expect(quotedRelation("public", "Mixed Case Table", notesFor({ driver: "postgres" }))).toBe(
      '"public"."Mixed Case Table"',
    )
    expect(quotedRelation("", "notes", notesFor({ driver: "sqlite" }))).toBe('"notes"')
  })
})

describe("the SELECT handed to Query", () => {
  test("is written in each engine's own dialect, and asks for nothing of the server", () => {
    expect(selectStatement({ driver: "postgres" }, "public", "orders")).toBe(
      'SELECT * FROM "public"."orders" LIMIT 100',
    )
    expect(selectStatement({ driver: "mysql" }, "blog", "posts")).toBe(
      "SELECT * FROM `blog`.`posts` LIMIT 100",
    )
    expect(selectStatement({ driver: "sqlite" }, "main", "notes")).toBe(
      'SELECT * FROM "main"."notes" LIMIT 100',
    )
    expect(selectStatement({ driver: "sqlserver" }, "sales", "orders")).toBe(
      "SELECT TOP (100) * FROM [sales].[orders]",
    )
    expect(selectStatement({ driver: "clickhouse" }, "analytics", "page_views")).toBe(
      "SELECT * FROM `analytics`.`page_views` LIMIT 100",
    )
    expect(selectStatement({ driver: "oracle" }, "APP", "ORDERS")).toBe(
      'SELECT * FROM "APP"."ORDERS" FETCH FIRST 100 ROWS ONLY',
    )
  })

  test("a mixed-case or dotted name is one name, not two", () => {
    expect(selectStatement({ driver: "postgres" }, "public", "odd.name")).toBe(
      'SELECT * FROM "public"."odd.name" LIMIT 100',
    )
  })
})
