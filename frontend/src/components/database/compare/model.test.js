import { describe, expect, test } from "bun:test"
import {
  canHandToQuery,
  changeCounts,
  comparableConnections,
  definitionRows,
  groupObjects,
  partsLine,
  readComparison,
  readSideId,
  scriptFilename,
  stepCounts,
  stepsLine,
  summaryLine,
} from "./model"

const table = (name, change, parts = []) => ({ kind: "table", name, change, parts })

describe("an answer is read whatever came back", () => {
  test("nothing at all is an empty comparison", () => {
    for (const answer of [null, undefined, {}, []]) {
      const read = readComparison(answer)
      expect(read.objects).toEqual([])
      expect(read.steps).toEqual([])
      expect(read.script).toBe("")
      expect(read.notes).toEqual([])
    }
  })

  test("an object that says nothing it can be told by is dropped, and a part keeps its sides", () => {
    const read = readComparison({
      from: {
        id: 1,
        name: "shop",
        schema: "public",
        flavor: "postgres",
        flavorLabel: "PostgreSQL",
      },
      to: { id: 2, name: "shop-staging", schema: "staging" },
      objects: [
        {
          kind: "table",
          name: "users",
          change: "changed",
          parts: [
            { part: "column", name: "email", change: "changed", from: "text", to: "text NOT NULL" },
          ],
        },
        { kind: "table", change: "added" },
        { kind: "table", name: "odd", change: "renamed" },
      ],
      steps: [
        {
          title: "Add column email to users",
          statements: ["ALTER TABLE users ADD COLUMN email text"],
        },
        { title: "Drop table t", destructive: true },
      ],
    })
    expect(read.from.name).toBe("shop")
    expect(read.to.flavorLabel).toBe("")
    expect(read.objects).toHaveLength(1)
    expect(read.objects[0].parts[0]).toEqual({
      part: "column",
      name: "email",
      change: "changed",
      from: "text",
      to: "text NOT NULL",
    })
    expect(read.steps[0]).toMatchObject({ destructive: false, manual: false, note: "" })
    expect(read.steps[1]).toMatchObject({ destructive: true, statements: [] })
  })
})

describe("the differences are grouped and counted", () => {
  const objects = [
    { kind: "function", name: "f(int)", change: "added", parts: [] },
    table("b", "removed"),
    { kind: "view", name: "v", change: "changed", parts: [] },
    table("a", "added"),
    { kind: "tablespace", name: "t", change: "added", parts: [] },
  ]

  test("by kind, in the page's order, a kind it does not know last", () => {
    const groups = groupObjects(objects)
    expect(groups.map((g) => g.label)).toEqual(["Tables", "Views", "Functions", "tablespace"])
    expect(groups[0].objects.map((o) => o.name)).toEqual(["b", "a"])
    expect(groupObjects([])).toEqual([])
  })

  test("by change", () => {
    expect(changeCounts(objects)).toEqual({ added: 3, removed: 1, changed: 1 })
  })

  test("in a line that names only what there is", () => {
    expect(summaryLine(objects)).toBe("5 differences: 3 added, 1 removed, 1 changed")
    expect(summaryLine([table("a", "added")])).toBe("1 difference: 1 added")
    expect(summaryLine([])).toBe("0 differences")
  })

  test("what changed inside an object, by part", () => {
    const parts = [
      { part: "column", name: "a", change: "added" },
      { part: "column", name: "b", change: "removed" },
      { part: "index", name: "i", change: "added" },
      { part: "foreign key", name: "f", change: "changed" },
    ]
    expect(partsLine(parts)).toBe("2 columns · 1 index · 1 foreign key")
    expect(partsLine([])).toBe("")
  })
})

describe("the migration is counted", () => {
  const steps = [
    { title: "a", statements: ["x", "y"], destructive: false, manual: false, note: "" },
    { title: "b", statements: ["z"], destructive: true, manual: false, note: "" },
    { title: "c", statements: [], destructive: false, manual: true, note: "do it" },
  ]

  test("by step, statement, and what needs care", () => {
    expect(stepCounts(steps)).toEqual({ steps: 3, statements: 3, destructive: 1, manual: 1 })
  })

  test("in a line", () => {
    expect(stepsLine(steps)).toBe("3 steps · 1 destructive · 1 for you to do")
    expect(stepsLine(steps.slice(0, 1))).toBe("1 step")
    expect(stepsLine([])).toBe("0 steps")
  })
})

describe("two definitions side by side", () => {
  test("alike lines face each other and a rewritten one is a row of its own", () => {
    const rows = definitionRows(
      "SELECT id,\n  email\nFROM users",
      "SELECT id,\n  email,\n  nickname\nFROM users",
    )
    expect(rows.map((r) => [r.from, r.to, r.same])).toEqual([
      ["SELECT id,", "SELECT id,", true],
      ["  email", "  email,", false],
      [null, "  nickname", false],
      ["FROM users", "FROM users", true],
    ])
  })

  test("trailing space is not a difference", () => {
    expect(definitionRows("a  \nb", "a\nb").every((r) => r.same)).toBe(true)
  })

  test("a side with nothing has no lines to line up", () => {
    expect(definitionRows("", "a\nb").map((r) => [r.from, r.to])).toEqual([
      [null, "a"],
      [null, "b"],
    ])
    expect(definitionRows("a", "")).toEqual([{ from: "a", to: null, same: false }])
    expect(definitionRows("", "")).toEqual([])
  })

  test("removed lines are taken before added ones face them", () => {
    const rows = definitionRows("a\nold1\nold2\nz", "a\nnew\nz")
    expect(rows.map((r) => [r.from, r.to])).toEqual([
      ["a", "a"],
      ["old1", "new"],
      ["old2", null],
      ["z", "z"],
    ])
  })

  test("a very long text is shown as it is, line for line", () => {
    const long = Array.from({ length: 1600 }, (_, i) => `line ${i}`).join("\n")
    const rows = definitionRows(long, `${long}\nextra`)
    expect(rows).toHaveLength(1601)
    expect(rows[1600]).toEqual({ from: null, to: "extra", same: false })
  })
})

describe("the file the migration is saved as", () => {
  test("names both sides", () => {
    expect(
      scriptFilename({
        from: { name: "Shop (prod)", schema: "public" },
        to: { name: "shop-staging", schema: "staging" },
      }),
    ).toBe("shop-prod-public-to-shop-staging-staging.sql")
    expect(scriptFilename({ from: { name: "", schema: "" }, to: { name: "x", schema: "" } })).toBe(
      "from-to-x.sql",
    )
  })
})

describe("the connections a schema can be compared with", () => {
  const c = (id, name, driver, extra = {}) => ({
    id,
    name,
    driver,
    environment: "",
    readOnly: false,
    ...extra,
  })
  const list = [
    c(3, "zeta", "postgres"),
    c(1, "alpha", "postgres"),
    c(2, "mongo", "mongodb"),
    c(4, "gone", "postgres", { broken: true }),
    c(5, "crdb", "postgres"),
  ]

  test("the same kind of engine, able to be compared and opened, by name", () => {
    const found = comparableConnections(
      list,
      { driver: "postgres" },
      (conn) => conn.name !== "crdb",
    )
    expect(found.map((x) => x.name)).toEqual(["alpha", "zeta"])
  })

  test("an address names a connection or none", () => {
    expect(readSideId("12")).toBe(12)
    for (const text of ["", "0", "012", "1e3", "-1", "two", "1 2"])
      expect(readSideId(text)).toBeUndefined()
  })
})

describe("handing the script to Query", () => {
  test("a short one goes in the address and a long one is pasted", () => {
    expect(canHandToQuery("CREATE TABLE t (id int);")).toBe(true)
    expect(canHandToQuery("")).toBe(true)
    expect(canHandToQuery("x".repeat(6900))).toBe(true)
    expect(canHandToQuery("x".repeat(7100))).toBe(false)
  })

  test("what the address makes longer is counted", () => {
    // Every quote and bracket becomes three characters of the address.
    expect(canHandToQuery('"("'.repeat(1000))).toBe(false)
  })
})
