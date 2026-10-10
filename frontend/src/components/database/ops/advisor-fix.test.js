import { describe, expect, test } from "bun:test"
import {
  aboutWords,
  applyVerdict,
  countCategories,
  countLevels,
  findingKeys,
  isCategory,
  isLevel,
  keptFindings,
  kindWord,
  maintenanceFix,
  statementsOf,
  targetName,
  buildsIndex,
  concurrently,
} from "./advisor-fix"

const finding = (id, level, category, extra = {}) => ({
  id,
  level,
  category,
  title: id,
  detail: "",
  ...extra,
})

const FINDINGS = [
  finding("connections-near-limit", "critical", "performance"),
  finding("no-backup", "warning", "reliability"),
  finding("unindexed-foreign-key", "warning", "performance"),
  finding("slow-log-off", "notice", "maintenance"),
]

describe("the report's counts and filters", () => {
  test("findings are counted by severity and by category", () => {
    expect(countLevels(FINDINGS)).toEqual({ critical: 1, warning: 2, notice: 1 })
    expect(countCategories(FINDINGS)).toEqual({
      security: 0,
      reliability: 1,
      performance: 2,
      maintenance: 1,
    })
  })

  test("a severity and a category each narrow, and together narrow further", () => {
    expect(keptFindings(FINDINGS, { level: "warning" }).map((f) => f.id)).toEqual([
      "no-backup",
      "unindexed-foreign-key",
    ])
    expect(
      keptFindings(FINDINGS, { level: "warning", category: "performance" }).map((f) => f.id),
    ).toEqual(["unindexed-foreign-key"])
    expect(keptFindings(FINDINGS, {}).length).toBe(4)
  })

  test("a word from the address that names no severity or category is no filter", () => {
    expect(isLevel("warning")).toBe(true)
    expect(isLevel("fatal")).toBe(false)
    expect(isCategory("security")).toBe(true)
    expect(isCategory("schema")).toBe(false)
  })

  test("one check reporting twice is two findings", () => {
    const twice = [
      finding("no-primary-key", "warning", "reliability"),
      finding("no-primary-key", "warning", "reliability"),
    ]
    const keys = findingKeys(twice)
    expect(new Set(keys).size).toBe(2)
    expect(keys[0]).toBe("no-primary-key")
  })
})

describe("what a finding is about", () => {
  test("one object is named, several are counted in their kind's word", () => {
    expect(aboutWords({ targets: [{ kind: "table", schema: "public", name: "audit_log" }] })).toBe(
      "public.audit_log",
    )
    expect(
      aboutWords({
        targets: [
          { kind: "index", schema: "public", name: "a" },
          { kind: "index", schema: "public", name: "b" },
        ],
      }),
    ).toBe("2 indexes")
    expect(
      aboutWords({
        targets: [
          { kind: "role", name: "a" },
          { kind: "setting", name: "b" },
        ],
      }),
    ).toBe("2 objects")
    expect(aboutWords({ objects: ["ClickHouse 24.8"] })).toBe("ClickHouse 24.8")
    expect(aboutWords({})).toBe("")
  })

  test("a target is named with its schema where it has one", () => {
    expect(targetName({ kind: "setting", name: "fsync" })).toBe("fsync")
    expect(targetName({ kind: "table", schema: "main", name: "notes" })).toBe("main.notes")
    expect(kindWord("role", 2)).toBe("accounts")
    expect(kindWord("no-such-kind")).toBe("object")
  })
})

describe("a fix that is one of the engine's maintenance actions", () => {
  const POSTGRES = [
    {
      id: "vacuum_analyze",
      label: "Vacuum and analyze",
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
      id: "analyze",
      label: "Analyze",
      description: "",
      scope: "either",
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
  const ORACLE = [
    {
      id: "gather_stats",
      label: "Gather statistics",
      description: "",
      scope: "either",
      requires: "service.control",
    },
  ]
  const orders = { kind: "table", schema: "public", name: "orders" }

  test("is run on the table the finding names", () => {
    expect(maintenanceFix({ id: "dead-rows" }, orders, POSTGRES).request).toEqual({
      action: "vacuum_analyze",
      schema: "public",
      table: "orders",
    })
  })

  test("uses each engine's own word for the same job", () => {
    expect(maintenanceFix({ id: "never-analysed" }, orders, POSTGRES).action.id).toBe("analyze")
    expect(maintenanceFix({ id: "never-analysed" }, orders, ORACLE).action.id).toBe("gather_stats")
  })

  test("carries the option the fix means, where the action has it", () => {
    const index = { kind: "index", schema: "public", name: "orders_idx", table: "orders" }
    expect(maintenanceFix({ id: "invalid-index" }, index, POSTGRES).request).toEqual({
      action: "reindex",
      schema: "public",
      table: "orders",
      index: "orders_idx",
      options: { concurrently: true },
    })
    const file = { kind: "database", name: "/srv/notes.db" }
    expect(maintenanceFix({ id: "wal-large" }, file, SQLITE).request).toEqual({
      action: "wal_checkpoint",
      options: { mode: "truncate" },
    })
  })

  test("is nothing for a check that is not maintenance, or an engine without the action", () => {
    expect(maintenanceFix({ id: "unindexed-foreign-key" }, orders, POSTGRES)).toBeUndefined()
    expect(maintenanceFix({ id: "dead-rows" }, orders, SQLITE)).toBeUndefined()
    expect(maintenanceFix({ id: "dead-rows" }, orders, [])).toBeUndefined()
    // A whole-file action is not run on one table, nor an index fix without its table.
    expect(maintenanceFix({ id: "free-pages" }, orders, SQLITE)).toBeUndefined()
    expect(
      maintenanceFix({ id: "invalid-index" }, { kind: "index", name: "i" }, POSTGRES),
    ).toBeUndefined()
    // A name every object inherits is not a check.
    expect(maintenanceFix({ id: "constructor" }, orders, POSTGRES)).toBeUndefined()
  })

  test("is applied from here only when the engine marks it as locking nothing", () => {
    const who = { readOnly: false, mayControl: true }
    const safe = maintenanceFix({ id: "dead-rows" }, orders, POSTGRES)
    expect(applyVerdict({ ...who, maintenance: safe })).toMatchObject({
      apply: true,
      via: "maintenance",
    })
    const locking = maintenanceFix({ id: "free-pages" }, { kind: "database", name: "f" }, SQLITE)
    const verdict = applyVerdict({ ...who, maintenance: locking })
    expect(verdict.apply).toBe(false)
    expect(verdict.reason).toContain("locks what it works on")
  })
})

describe("whether a statement is applied from here", () => {
  const who = { readOnly: false, mayControl: true }

  test("only when the server's classifier says it destroys nothing", () => {
    expect(applyVerdict({ ...who, destructive: false })).toEqual({ apply: true, via: "statement" })
    const refused = applyVerdict({ ...who, destructive: true })
    expect(refused.apply).toBe(false)
    expect(refused.reason).toContain("Query page")
  })

  test("not before the server has answered", () => {
    expect(applyVerdict({ ...who })).toEqual({ apply: false, reason: "" })
  })

  test("never on a protected connection, and never for a role that may not", () => {
    expect(applyVerdict({ readOnly: true, mayControl: true, destructive: false }).apply).toBe(false)
    expect(applyVerdict({ readOnly: false, mayControl: false, destructive: false }).apply).toBe(
      false,
    )
  })
})

test("a fix is its statements, one to a line", () => {
  expect(statementsOf("CREATE INDEX a ON t (x);\n\nCREATE INDEX b ON t (y);")).toEqual([
    "CREATE INDEX a ON t (x);",
    "CREATE INDEX b ON t (y);",
  ])
})

describe("a fix that builds an index", () => {
  test("is told from one that does not", () => {
    expect(
      buildsIndex('CREATE INDEX "orders_customer_id_idx" ON "public"."orders" ("customer_id");'),
    ).toBe(true)
    expect(buildsIndex("create unique index i on t (x)")).toBe(true)
    expect(buildsIndex("CREATE INDEX CONCURRENTLY i ON t (x)")).toBe(false)
    expect(buildsIndex('DROP INDEX "public"."i";')).toBe(false)
    expect(buildsIndex("ALTER TABLE t ADD PRIMARY KEY (id)")).toBe(false)
  })

  test("is the same index built alongside the table's writes", () => {
    expect(concurrently('CREATE INDEX "i" ON "public"."orders" ("customer_id");')).toBe(
      'CREATE INDEX CONCURRENTLY "i" ON "public"."orders" ("customer_id");',
    )
    expect(concurrently("CREATE UNIQUE INDEX i ON t (x)")).toBe(
      "CREATE UNIQUE INDEX CONCURRENTLY i ON t (x)",
    )
    // Already concurrent, or not an index at all: left as the server wrote it.
    expect(concurrently("CREATE INDEX CONCURRENTLY i ON t (x)")).toBe(
      "CREATE INDEX CONCURRENTLY i ON t (x)",
    )
    expect(concurrently("VACUUM t")).toBe("VACUUM t")
  })
})
