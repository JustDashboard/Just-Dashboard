import { describe, expect, test } from "bun:test"
import {
  canCutOver,
  copyFinished,
  copyShare,
  importNameProblem,
  importStep,
  importSteps,
  importTitle,
  isLive,
  lagWords,
  progressWords,
  EMPTY_SOURCE,
  sourceDsn,
  sourceTotals,
  statusWords,
  suggestedDatabase,
  tableLine,
  tableStateWords,
} from "./model"

const record = (patch = {}) => ({
  id: 1,
  name: "jd_import_shop",
  mode: "replication",
  status: "replicating",
  database: "shop_copy",
  source: { address: "db.example.com:5432", user: "reader", database: "shop" },
  structureOnly: false,
  publicationMade: true,
  startedBy: "ana",
  createdAt: "2026-10-17T10:00:00Z",
  updatedAt: "2026-10-17T10:00:00Z",
  ...patch,
})

const subscription = (states) => ({
  exists: true,
  enabled: true,
  applying: true,
  errors: 0,
  ready: states.filter((state) => state === "ready").length,
  tables: states.map((state, index) => ({
    schema: "public",
    name: `t${index}`,
    state,
    rowsCopied: -1,
  })),
})

describe("the spine", () => {
  test("a replication ends in a cutover and a stream in its copy", () => {
    expect(importSteps("replication").map((step) => step.key)).toEqual([
      "source",
      "plan",
      "copy",
      "cutover",
    ])
    expect(importSteps("stream").map((step) => step.key)).toEqual(["source", "plan", "copy"])
  })

  test("it follows the state the server keeps", () => {
    expect(importStep("replication", { inspected: false })).toBe(0)
    expect(importStep("replication", { inspected: true })).toBe(1)
    expect(
      importStep("replication", { inspected: true, import: record({ status: "running" }) }),
    ).toBe(2)
    expect(importStep("replication", { inspected: true, import: record() })).toBe(2)
    expect(importStep("replication", { inspected: true, import: record(), caughtUp: true })).toBe(3)
    expect(
      importStep("replication", { inspected: true, import: record({ status: "cutting" }) }),
    ).toBe(3)
    expect(importStep("replication", { inspected: true, import: record({ status: "done" }) })).toBe(
      4,
    )
    expect(
      importStep("stream", { inspected: true, import: record({ mode: "stream", status: "done" }) }),
    ).toBe(3)
    // An import that failed is still on the copy, where its reason is.
    expect(
      importStep("replication", { inspected: true, import: record({ status: "failed" }) }),
    ).toBe(2)
  })
})

describe("words", () => {
  test("a status has a word and a tone, and only three hold something", () => {
    expect(statusWords("cutting")).toEqual({ word: "cutting over", tone: "notice" })
    expect(statusWords("cancelled").word).toBe("stopped")
    expect(["running", "replicating", "cutting"].every(isLive)).toBe(true)
    expect(["done", "failed", "cancelled"].some(isLive)).toBe(false)
  })

  test("how far behind the target is", () => {
    expect(lagWords(undefined)).toBe("—")
    expect(lagWords({ exists: false, active: false, bytes: 0 })).toBe("no slot")
    expect(lagWords({ exists: true, active: true, bytes: 0 })).toBe("caught up")
    expect(lagWords({ exists: true, active: true, bytes: 3 * 1024 * 1024 })).toBe("3.0 MB behind")
  })

  test("a table's state is a word the page does not have to invent", () => {
    expect(tableStateWords("synchronizing")).toBe("catching up")
    expect(tableLine({ schema: "public", name: "orders" })).toBe("orders")
    expect(tableLine({ schema: "shop", name: "orders" })).toBe("shop.orders")
  })

  test("the title says from where to where", () => {
    expect(importTitle(record())).toBe("shop on db.example.com:5432 → shop_copy")
    expect(importTitle({ source: { address: "cache:6379" }, database: "3" })).toBe("cache:6379 → 3")
  })

  test("an engine that stages its dump says which half it is on", () => {
    const at = (phase, bytes = 0) => ({
      phase,
      tables: 0,
      tablesDone: 0,
      rows: 0,
      estimatedRows: 0,
      bytes,
    })
    expect(progressWords(at("dumping"))).toBe("Reading the source into a dump")
    expect(progressWords(at("loading", 3 * 1024 * 1024))).toBe("Loading the dump · 3.0 MB")
  })

  test("a streamed copy is told in the figures it has", () => {
    expect(progressWords(undefined)).toBe("Starting")
    expect(
      progressWords({
        phase: "copying",
        tables: 0,
        tablesDone: 0,
        rows: 0,
        estimatedRows: 0,
        bytes: 0,
      }),
    ).toBe("Starting")
    expect(
      progressWords({
        phase: "copying",
        tables: 12,
        tablesDone: 3,
        rows: 1200,
        estimatedRows: 5000,
        bytes: 2048,
      }),
    ).toBe("3 of 12 tables · 1,200 rows · 2.0 KB read")
  })
})

describe("how far a copy has got", () => {
  test("a replication counts its tables", () => {
    const live = {
      import: record(),
      subscription: subscription(["ready", "copying", "waiting", "ready"]),
    }
    expect(copyShare(live)).toBe(50)
    expect(copyFinished(live)).toBe(false)
    expect(copyShare({ import: record() })).toBeUndefined()
  })

  test("a stream counts its rows against an estimate, and never claims to be done", () => {
    const stream = record({ mode: "stream", status: "running" })
    const at = (rows, estimatedRows, tables = 4, tablesDone = 1) => ({
      import: stream,
      progress: { phase: "copying", tables, tablesDone, rows, estimatedRows, bytes: 0 },
    })
    expect(copyShare(at(250, 1000))).toBe(25)
    expect(copyShare(at(5000, 1000))).toBe(99)
    // With no estimate it falls back to the tables.
    expect(copyShare(at(250, 0, 4, 1))).toBe(25)
    expect(copyShare({ import: stream })).toBeUndefined()
  })

  test("a cutover is asked for only once every table is ready", () => {
    const copying = { import: record(), subscription: subscription(["ready", "copying"]) }
    const ready = { import: record(), subscription: subscription(["ready", "ready"]) }
    expect(canCutOver(copying)).toBe(false)
    expect(canCutOver(ready)).toBe(true)
    expect(canCutOver({ ...ready, import: record({ status: "cutting" }) })).toBe(false)
    expect(canCutOver({ ...ready, import: record({ mode: "stream" }) })).toBe(false)
    expect(canCutOver({ import: record(), subscription: subscription([]) })).toBe(false)
    expect(canCutOver({ import: record() })).toBe(false)
  })
})

describe("names", () => {
  test("a publication's name is one the server accepts", () => {
    expect(importNameProblem("jd_import_shop")).toBeUndefined()
    expect(importNameProblem("Shop")).toBeDefined()
    expect(importNameProblem("1shop")).toBeDefined()
    expect(importNameProblem("a".repeat(51))).toBeDefined()
    expect(importNameProblem("")).toBeDefined()
  })

  test("the new database is the source's name, unless that is taken", () => {
    expect(suggestedDatabase("shop", ["postgres"])).toBe("shop")
    expect(suggestedDatabase("shop", ["Shop"])).toBe("shop_2")
    expect(suggestedDatabase("shop", ["shop", "shop_2"])).toBe("shop_3")
    expect(suggestedDatabase("my-shop!", [])).toBe("my_shop")
    expect(suggestedDatabase("2024", [])).toBe("db_2024")
    expect(suggestedDatabase("", [])).toBe("imported")
  })
})

describe("what a source holds", () => {
  test("a table nobody counted is not a table of no rows", () => {
    const totals = sourceTotals([
      { estimatedRows: 100, size: 10 },
      { estimatedRows: -1, size: 5 },
    ])
    expect(totals).toEqual({ tables: 2, rows: 100, size: 15, uncounted: true })
    expect(sourceTotals([]).uncounted).toBe(false)
  })
})

describe("the source's address", () => {
  test("fields are written as the engine's own string, and nothing is sent until there is a host", () => {
    expect(sourceDsn("postgres", EMPTY_SOURCE)).toBe("")
    const typed = {
      ...EMPTY_SOURCE,
      fields: {
        ...EMPTY_SOURCE.fields,
        host: "db.example.com",
        user: "reader",
        password: "p@ss",
        database: "shop",
      },
    }
    const dsn = sourceDsn("postgres", typed)
    expect(dsn.startsWith("postgres://reader:p%40ss@db.example.com")).toBe(true)
    expect(dsn).toContain("/shop")
  })

  test("a string pasted whole is used as it is", () => {
    const pasted = { ...EMPTY_SOURCE, shape: "url", pasted: " postgres://u:p@h:5432/d " }
    expect(sourceDsn("postgres", pasted)).toBe("postgres://u:p@h:5432/d")
    expect(sourceDsn("postgres", { ...pasted, pasted: "   " })).toBe("")
  })
})
