import { describe, expect, test } from "bun:test"
import {
  byAction,
  confirmsFirst,
  countFlags,
  counted,
  deadShare,
  fittedColumns,
  flaggedBytes,
  indexFlags,
  lastRun,
  locksThroughout,
  maintenanceVerbs,
  needsVacuum,
  neverAnalysed,
  objectParam,
  parseObject,
  schemasOf,
  seqShare,
  sortTables,
  tableColumns,
  targetWords,
} from "./performance-storage"

const table = (name, extra = {}) => ({
  schema: "public",
  table: name,
  rows: 100,
  deadRows: -1,
  totalBytes: 1000,
  tableBytes: 800,
  indexBytes: 200,
  toastBytes: 0,
  bloatBytes: -1,
  seqScans: -1,
  seqRowsRead: -1,
  indexScans: -1,
  indexRowsRead: -1,
  inserts: -1,
  updates: -1,
  deletes: -1,
  modsSinceAnalyze: -1,
  ...extra,
})

describe("a table's place in the address", () => {
  test("a dot or a slash in a name is not a separator", () => {
    const param = objectParam("my.schema", "a/b table")
    expect(parseObject(param)).toEqual({ schema: "my.schema", name: "a/b table" })
  })

  test("what is not such a place is nothing", () => {
    expect(parseObject("")).toBeNull()
    expect(parseObject("orders")).toBeNull()
    expect(parseObject("public/%E0%A4%A")).toBeNull()
  })
})

describe("what an engine does not keep", () => {
  test("is never a zero", () => {
    expect(counted(-1)).toBeUndefined()
    expect(counted(undefined)).toBeUndefined()
    expect(counted(0)).toBe(0)
  })

  test("leaves its column out of the table", () => {
    expect([...tableColumns([table("t")])]).toEqual([])
    const postgres = table("t", {
      deadRows: 0,
      bloatBytes: 0,
      seqScans: 2,
      indexScans: 0,
      lastAutovacuum: "2026-10-01T00:00:00Z",
      lastAnalyze: "2026-10-01T00:00:00Z",
    })
    expect([...tableColumns([postgres])].sort()).toEqual([
      "analyze",
      "bloat",
      "dead",
      "scans",
      "vacuum",
    ])
    const analytic = table("t", { engine: "MergeTree", parts: 3, uncompressedBytes: 4000 })
    expect([...tableColumns([analytic])].sort()).toEqual(["compression", "engine", "parts"])
  })

  test("sorts last whichever way the column runs", () => {
    const tables = [
      table("a", { deadRows: -1 }),
      table("b", { deadRows: 5 }),
      table("c", { deadRows: 90 }),
    ]
    expect(sortTables(tables, "dead", true).map((t) => t.table)).toEqual(["c", "b", "a"])
    expect(sortTables(tables, "dead", false).map((t) => t.table)).toEqual(["b", "c", "a"])
  })
})

describe("a table's housekeeping", () => {
  test("the newer of a manual run and the engine's own is the one shown", () => {
    expect(lastRun("2026-10-01T10:00:00Z", "2026-10-01T09:00:00Z")).toEqual({
      at: "2026-10-01T10:00:00Z",
      automatic: false,
    })
    expect(lastRun(undefined, "2026-10-01T09:00:00Z")).toEqual({
      at: "2026-10-01T09:00:00Z",
      automatic: true,
    })
    expect(lastRun(undefined, undefined)).toBeUndefined()
  })

  test("a vacuum is due past the advisor's own line", () => {
    expect(needsVacuum(table("t", { rows: 20_000, deadRows: 40_000 }))).toBe(true)
    // Many dead rows that are a sliver of a large table are not.
    expect(needsVacuum(table("t", { rows: 5_000_000, deadRows: 40_000 }))).toBe(false)
    expect(needsVacuum(table("t", { rows: 100, deadRows: 900 }))).toBe(false)
    expect(needsVacuum(table("t", { deadRows: -1 }))).toBe(false)
    expect(deadShare(table("t", { rows: 60, deadRows: 40 }))).toBe(0.4)
    expect(deadShare(table("t"))).toBeUndefined()
  })

  test("never analysed is said only of a table with rows, on an engine that keeps the date", () => {
    const columns = new Set(["analyze"])
    expect(neverAnalysed(table("t", { rows: 5000 }), columns)).toBe(true)
    expect(neverAnalysed(table("t", { rows: 0, inserts: 0 }), columns)).toBe(false)
    expect(neverAnalysed(table("t", { lastAutoanalyze: "2026-10-01T00:00:00Z" }), columns)).toBe(
      false,
    )
    expect(neverAnalysed(table("t", { rows: 5000 }), new Set())).toBe(false)
  })

  test("sequential scans are a share of every scan", () => {
    expect(seqShare(table("t", { seqScans: 3, indexScans: 1 }))).toBe(0.75)
    expect(seqShare(table("t", { seqScans: 0, indexScans: 0 }))).toBeUndefined()
    expect(seqShare(table("t"))).toBeUndefined()
  })
})

test("the schemas a list spans are counted, by name", () => {
  expect(schemasOf([{ schema: "public" }, { schema: "analytics" }, { schema: "public" }])).toEqual([
    { name: "analytics", count: 1 },
    { name: "public", count: 2 },
  ])
})

describe("what is wrong with an index", () => {
  const index = (name, extra = {}) => ({
    schema: "public",
    table: "orders",
    name,
    columns: ["status"],
    unique: false,
    primary: false,
    valid: true,
    bytes: 1000,
    scans: 0,
    rowsRead: 0,
    unused: false,
    ...extra,
  })

  test("is listed worst first, and counted", () => {
    const copy = index("copy", { duplicateOf: "orig", unused: true, bytes: 400 })
    const broken = index("broken", { valid: false })
    const narrow = index("narrow", { coveredBy: "wide", bytes: 50 })
    const fine = index("pkey", { primary: true, unique: true })
    expect(indexFlags(copy)).toEqual(["duplicate", "unused"])
    expect(indexFlags(fine)).toEqual([])
    expect(countFlags([copy, broken, narrow, fine])).toEqual({
      invalid: 1,
      duplicate: 1,
      covered: 1,
      unused: 1,
    })
    expect(flaggedBytes([copy, broken, narrow, fine], "unused")).toBe(400)
  })
})

describe("the maintenance a target is offered", () => {
  const POSTGRES = [
    {
      id: "vacuum",
      label: "Vacuum",
      description: "",
      scope: "either",
      requires: "service.control",
    },
    {
      id: "analyze",
      label: "Analyze",
      description: "",
      scope: "either",
      requires: "service.control",
    },
    {
      id: "vacuum_full",
      label: "Vacuum full",
      description: "",
      scope: "either",
      blocking: true,
      requires: "destructive",
    },
    {
      id: "reindex",
      label: "Reindex",
      description: "",
      scope: "either",
      blocking: true,
      requires: "destructive",
      options: ["concurrently"],
    },
  ]
  const SQLITE = [
    {
      id: "integrity_check",
      label: "Integrity check",
      description: "",
      scope: "database",
      readOnly: true,
      requires: "service.control",
    },
    {
      id: "wal_checkpoint",
      label: "Checkpoint",
      description: "",
      scope: "database",
      requires: "service.control",
      options: ["mode"],
    },
    {
      id: "vacuum",
      label: "Vacuum",
      description: "",
      scope: "database",
      blocking: true,
      requires: "destructive",
    },
  ]
  const admin = { can: () => true, readOnly: false }
  const operator = { can: (capability) => capability !== "destructive", readOnly: false }
  const orders = { schema: "public", table: "orders" }

  test("a role sees only what its capability covers", () => {
    expect(maintenanceVerbs(POSTGRES, orders, operator).map((v) => v.key)).toEqual([
      "vacuum",
      "analyze",
    ])
    expect(maintenanceVerbs(POSTGRES, orders, admin).map((v) => v.key)).toEqual([
      "vacuum",
      "analyze",
      "vacuum_full",
      "reindex",
      "reindex:concurrently",
    ])
    expect(maintenanceVerbs(POSTGRES, orders, { can: () => false, readOnly: false })).toEqual([])
  })

  test("a protected connection keeps only the checks that change nothing", () => {
    const who = { can: () => true, readOnly: true }
    expect(maintenanceVerbs(POSTGRES, orders, who)).toEqual([])
    expect(maintenanceVerbs(SQLITE, {}, who).map((v) => v.key)).toEqual(["integrity_check"])
  })

  test("an action is offered where its scope fits the target", () => {
    // A whole-file action is not a table's, and a table's is not the database's.
    expect(maintenanceVerbs(SQLITE, { schema: "main", table: "notes" }, admin)).toEqual([])
    const tableOnly = [
      {
        id: "optimize",
        label: "Optimize",
        description: "",
        scope: "table",
        requires: "service.control",
        options: ["final"],
      },
    ]
    expect(maintenanceVerbs(tableOnly, {}, admin)).toEqual([])
    expect(maintenanceVerbs(tableOnly, orders, admin).map((v) => v.key)).toEqual([
      "optimize",
      "optimize:final",
    ])
  })

  test("an index is offered the actions that take one", () => {
    const verbs = maintenanceVerbs(POSTGRES, { ...orders, index: "orders_pkey" }, admin)
    expect(verbs.map((v) => v.key)).toEqual(["reindex", "reindex:concurrently"])
    expect(verbs[1].request).toEqual({
      action: "reindex",
      schema: "public",
      table: "orders",
      index: "orders_pkey",
      options: { concurrently: true },
    })
  })

  test("a choice inside an action is one verb per answer", () => {
    const verbs = maintenanceVerbs(SQLITE, {}, admin)
    expect(verbs.filter((v) => v.action.id === "wal_checkpoint").map((v) => v.label)).toEqual([
      "Checkpoint",
      "Checkpoint, passive",
      "Checkpoint, full",
      "Checkpoint, restart",
    ])
    expect(verbs.find((v) => v.key === "wal_checkpoint:truncate").request.options).toEqual({
      mode: "truncate",
    })
    expect(byAction(verbs).map((group) => group.length)).toEqual([1, 4, 1])
  })

  test("what locks is asked about first, and only its plain form locks throughout", () => {
    const verbs = maintenanceVerbs(POSTGRES, orders, admin)
    const byKey = Object.fromEntries(verbs.map((v) => [v.key, v]))
    expect(confirmsFirst(byKey.vacuum.action)).toBe(false)
    expect(confirmsFirst(byKey.vacuum_full.action)).toBe(true)
    expect(locksThroughout(byKey.reindex)).toBe(true)
    expect(locksThroughout(byKey["reindex:concurrently"])).toBe(false)
  })

  test("a run's target is said in words", () => {
    expect(targetWords({ action: "vacuum", schema: "public", table: "orders" }, "table")).toBe(
      "public.orders",
    )
    expect(targetWords({ action: "reindex", table: "orders", index: "orders_pkey" }, "table")).toBe(
      "index orders_pkey",
    )
    expect(targetWords({ action: "analyze" }, "table")).toBe("the whole database")
    expect(targetWords({ action: "reindex", schema: "public" }, "table")).toBe(
      "every table of public",
    )
  })
})

describe("the columns a table of a given width draws", () => {
  const postgres = new Set(["dead", "bloat", "scans", "vacuum", "analyze"])

  test("every column where there is the width for them", () => {
    expect([...fittedColumns(postgres, 1400)].sort()).toEqual([...postgres].sort())
  })

  test("go one at a time, what qualifies a figure before the figure", () => {
    // A laptop's width beside the rail: the scans go first, then the estimate.
    expect(fittedColumns(postgres, 968).has("scans")).toBe(false)
    expect(fittedColumns(postgres, 968).has("bloat")).toBe(true)
    const narrower = fittedColumns(postgres, 840)
    expect(narrower.has("bloat")).toBe(false)
    expect(narrower.has("vacuum")).toBe(true)
    expect(narrower.has("analyze")).toBe(true)
    const narrowest = fittedColumns(postgres, 600)
    expect([...narrowest]).toEqual(["dead"])
  })

  test("an engine that fills few columns keeps them all at a width another could not", () => {
    const mysql = new Set(["engine", "bloat"])
    expect([...fittedColumns(mysql, 600)].sort()).toEqual(["bloat", "engine"])
  })

  test("under the width of a table the rows are drawn down, and nothing is dropped", () => {
    expect(fittedColumns(postgres, 559)).toBeNull()
    expect(fittedColumns(postgres, 0)).toBeNull()
  })

  test("the set it is handed is not the one it changes", () => {
    fittedColumns(postgres, 600)
    expect(postgres.size).toBe(5)
  })
})
