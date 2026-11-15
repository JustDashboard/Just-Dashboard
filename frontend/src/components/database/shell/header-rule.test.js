import { describe, expect, test } from "bun:test"
import { databaseHead, fillsUnderHeader, isItemPage } from "./header-rule"

/** An engine with every page, as the registry marks them. */
const WORKBENCHES = new Set(["assistant", "data", "query", "schema", "diagram", "logs"])
const sql = (section) => ({ workbench: WORKBENCHES.has(section) })
/** A key-value store: no Schema, no Diagram, no Search. */
const redis = (section) =>
  ["schema", "diagram", "search", "generate"].includes(section) ? undefined : sql(section)

describe("what a page under a database opens on", () => {
  test("every reading page opens on the full header", () => {
    for (const page of [
      "",
      "/performance",
      "/access",
      "/alerts",
      "/backups",
      "/import",
      "/generate",
      "/search",
      "/settings",
      "/settings/network",
      "/settings/danger",
    ]) {
      expect(databaseHead(`/databases/1${page}`, sql)).toBe("full")
    }
  })

  test("the assistant and Logs are under it too, and take the height it leaves", () => {
    for (const page of ["/assistant", "/logs"]) {
      expect(databaseHead(`/databases/1${page}`, sql)).toBe("full")
      expect(fillsUnderHeader(`/databases/1${page}`, sql)).toBe(true)
    }
    expect(fillsUnderHeader("/databases/1/backups", sql)).toBe(false)
    expect(fillsUnderHeader("/databases/1", sql)).toBe(false)
  })

  test("a workbench heads itself, and the shell draws nothing over it", () => {
    for (const page of ["/data", "/query", "/schema", "/diagram"]) {
      expect(databaseHead(`/databases/1${page}`, sql)).toBe("head")
      expect(fillsUnderHeader(`/databases/1${page}`, sql)).toBe(false)
    }
  })

  test("where the engine has no such page, its statement stands under the header", () => {
    expect(databaseHead("/databases/4/schema", redis)).toBe("full")
    expect(databaseHead("/databases/4/data", redis)).toBe("head")
  })

  test("an item's page keeps the one-line strip over its own identity line", () => {
    for (const page of [
      "/performance/statements/s2",
      "/performance/tables/public.orders",
      "/performance/sessions/52",
      "/access/reporting",
      "/access/app%40%25",
    ]) {
      expect(isItemPage(`/databases/1${page}`)).toBe(true)
      expect(databaseHead(`/databases/1${page}`, sql)).toBe("strip")
    }
  })

  test("an address that is no page of the database is under the header, not an item", () => {
    for (const page of [
      "/no-such-page",
      "/performance/statements",
      "/performance/nothing/1",
      "/access/reporting/more",
      "/backups/2",
      "/settings/nothing",
    ]) {
      expect(isItemPage(`/databases/1${page}`)).toBe(false)
      expect(databaseHead(`/databases/1${page}`, sql)).toBe("full")
    }
    // Not a database at all: the layout never asks, and the answer is harmless.
    expect(isItemPage("/databases/map")).toBe(false)
    expect(isItemPage("/databases/007/access/app")).toBe(false)
  })
})
