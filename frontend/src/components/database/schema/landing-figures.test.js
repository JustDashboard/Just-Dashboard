import { describe, expect, test } from "bun:test"
import { schemaFigures } from "./landing-figures"

const table = (name, over = {}) => ({ kind: "table", schema: "public", name, ...over })
const stat = (name, over = {}) => ({
  schema: "public",
  table: name,
  rows: -1,
  deadRows: -1,
  totalBytes: 0,
  tableBytes: 0,
  indexBytes: 0,
  toastBytes: 0,
  bloatBytes: -1,
  seqScans: -1,
  indexScans: -1,
  ...over,
})

describe("what a schema amounts to", () => {
  test("the catalogue alone gives counts, and figures only where it carries them", () => {
    const figures = schemaFigures({
      objects: {
        tables: [
          table("orders", { estimatedRows: 9000, size: 3_000_000 }),
          table("items", { estimatedRows: 18000, size: 2_000_000 }),
        ],
        views: [{ kind: "view", schema: "public", name: "summary" }],
        functions: [{ kind: "function", schema: "public", name: "f" }],
      },
    })
    expect(figures.tables).toBe(2)
    expect(figures.others).toEqual([{ group: "views", count: 1 }])
    expect(figures.rows).toBe(27000)
    expect(figures.bytes).toBe(5_000_000)
    expect(figures.indexBytes).toBeNull()
    expect(figures.reclaimable).toBeNull()
    expect(figures.rankedBy).toBe("size")
    expect(figures.largest.map((entry) => entry.object.name)).toEqual(["orders", "items"])
    expect(figures.largest[1].share).toBeCloseTo(2 / 3)
  })

  test("the engine's statistics win, and split the bytes", () => {
    const figures = schemaFigures(
      {
        objects: {
          tables: [table("notes", { estimatedRows: -1 }), table("keyless", { estimatedRows: -1 })],
        },
      },
      [
        stat("notes", { rows: 2000, totalBytes: 307200, indexBytes: 24576, bloatBytes: 10709 }),
        stat("keyless", { rows: 3, totalBytes: 4096, indexBytes: 0, bloatBytes: 4062 }),
      ],
    )
    expect(figures.rows).toBe(2003)
    expect(figures.bytes).toBe(311296)
    expect(figures.indexBytes).toBe(24576)
    expect(figures.reclaimable).toBe(14771)
    expect(figures.measured).toBe(2)
    expect(figures.largest[0]).toMatchObject({ bytes: 307200, rows: 2000, share: 1 })
  })

  test("reads are added up only over the tables the engine counts them for", () => {
    const figures = schemaFigures({ objects: { tables: [table("a"), table("b"), table("c")] } }, [
      stat("a", { seqScans: 4, indexScans: 18003 }),
      stat("b", { seqScans: 3, indexScans: 0 }),
      stat("c", { seqScans: -1, indexScans: -1 }),
    ])
    expect(figures.reads).toEqual({ byIndex: 18003, byScan: 7 })
    expect(
      schemaFigures({ objects: { tables: [table("a")] } }, [
        stat("a", { seqScans: -1, indexScans: -1 }),
      ]).reads,
    ).toBeNull()
  })

  test("a figure nobody gave is absent, never zero", () => {
    const figures = schemaFigures({
      objects: { tables: [table("a", { estimatedRows: -1 }), table("b")], views: [] },
    })
    expect(figures.rows).toBeNull()
    expect(figures.bytes).toBeNull()
    expect(figures.rankedBy).toBeNull()
    expect(figures.largest).toEqual([])
    expect(figures.others).toEqual([])
  })

  test("where few tables have a size, rows rank them", () => {
    const figures = schemaFigures({
      objects: {
        tables: [
          table("a", { estimatedRows: 10, size: 100 }),
          table("b", { estimatedRows: 500 }),
          table("c", { estimatedRows: 20 }),
        ],
      },
    })
    expect(figures.rankedBy).toBe("rows")
    expect(figures.largest.map((entry) => entry.object.name)).toEqual(["b", "c", "a"])
  })

  test("an empty schema is no tables and nothing to rank", () => {
    const figures = schemaFigures({ objects: { tables: [], views: [] } })
    expect(figures).toMatchObject({ tables: 0, rows: null, bytes: null, largest: [] })
  })
})
