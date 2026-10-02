import { describe, expect, test } from "bun:test"
import { schemaSummary } from "./summary"

const table = (name, estimatedRows, size) => ({
  kind: "table",
  schema: "public",
  name,
  estimatedRows,
  size,
})

describe("what a schema amounts to", () => {
  test("counts by kind, rows and bytes added up, and the largest first", () => {
    const summary = schemaSummary({
      objects: {
        tables: [table("small", 10, 8192), table("big", 9000, 1 << 20), table("mid", 400, 65536)],
        views: [{ kind: "view", schema: "public", name: "v" }],
        materializedViews: [],
      },
    })
    expect(summary.counts).toEqual([
      { group: "tables", count: 3 },
      { group: "views", count: 1 },
    ])
    expect(summary.rows).toBe(9410)
    expect(summary.size).toBe(8192 + (1 << 20) + 65536)
    expect(summary.rankedBy).toBe("size")
    expect(summary.largest.map((entry) => entry.object.name)).toEqual(["big", "mid", "small"])
    expect(summary.largest[0].share).toBe(1)
    expect(summary.largest[1].share).toBeCloseTo(0.0625)
  })
  test("an engine that reports no sizes is ranked by its row estimates", () => {
    const summary = schemaSummary({
      objects: { tables: [table("a", 5), table("b", 50), table("c", -1)] },
    })
    expect(summary.rankedBy).toBe("rows")
    expect(summary.size).toBeNull()
    expect(summary.rows).toBe(55)
    expect(summary.largest.map((entry) => entry.object.name)).toEqual(["b", "a"])
  })
  test("unknown is not zero: with nothing measured there is no figure and no ranking", () => {
    const summary = schemaSummary({ objects: { tables: [table("a", -1), table("b")] } })
    expect(summary).toMatchObject({ rows: null, size: null, rankedBy: null, largest: [] })
    expect(summary.counts).toEqual([{ group: "tables", count: 2 }])
  })
  test("only the most are listed, and an empty table is not among the largest", () => {
    const tables = Array.from({ length: 12 }, (_, i) => table(`t${i}`, i, i * 100))
    const summary = schemaSummary({ objects: { tables } }, 5)
    expect(summary.largest.map((entry) => entry.object.name)).toEqual([
      "t11",
      "t10",
      "t9",
      "t8",
      "t7",
    ])
    expect(schemaSummary({ objects: { tables: [table("empty", 0, 0)] } }).largest).toEqual([])
  })
})
