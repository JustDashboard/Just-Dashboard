import { expect, test } from "bun:test"
import { schemaFacts, tableFacts } from "./table-facts"

const said = (facts) =>
  facts.map((fact) => [fact.lead, fact.text, fact.tail].filter(Boolean).join(" ")).join(" · ")

test("a table is one line: rows, size, how the size splits, whose it is", () => {
  expect(
    said(
      tableFacts({
        estimatedRows: 9000,
        size: 3355443,
        dataSize: 2411725,
        indexSize: 1015808,
        owner: "app",
      }),
    ),
  ).toBe("~9,000 rows · 3.2 MB · 2.3 MB data · 992.0 KB indexes · owned by app")
})

test("what an engine does not report is left out, and an uncounted table says so", () => {
  expect(said(tableFacts({ estimatedRows: -1 }))).toBe("rows not counted")
  expect(said(tableFacts({ estimatedRows: 1, size: 8192 }))).toBe("~1 row · 8.0 KB")
  // The split is said whole or not at all: half of it is a figure with no meaning.
  expect(said(tableFacts({ estimatedRows: 3, size: 8192, dataSize: 4096 }))).toBe(
    "~3 rows · 8.0 KB",
  )
})

test("the Structure tab counts the columns, and an engine's own facts follow in its words", () => {
  expect(
    said(
      tableFacts(
        { estimatedRows: 250, facts: [{ name: "Engine", value: "InnoDB" }], owner: "" },
        1,
      ),
    ),
  ).toBe("~250 rows · 1 column · engine InnoDB")
})

test("a schema is one line of what it holds", () => {
  expect(
    said(
      schemaFacts({
        counts: [
          { key: "tables", count: 2, one: "table", many: "tables" },
          { key: "views", count: 1, one: "view", many: "views" },
        ],
        rows: 1_200_250,
        size: 50331648,
      }),
    ),
  ).toBe("2 tables · 1 view · ~1.2M rows · 48.0 MB on disk")
  expect(said(schemaFacts({ counts: [], rows: null, size: null }))).toBe("")
})
