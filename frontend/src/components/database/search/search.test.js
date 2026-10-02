import { describe, expect, test } from "bun:test"
import {
  cellText,
  excerpt,
  groupHits,
  markParts,
  otherCells,
  readResult,
  rowFilters,
  tableFilters,
} from "./search"

const customer = (id, email, extra = {}) => ({
  schema: "public",
  table: "customers",
  column: "email",
  value: email,
  row: { id: String(id), email, tier: "pro", avatar: null, profile: '{"theme": "dark"}', ...extra },
})

describe("marking where the value occurs", () => {
  test("every occurrence, whatever its case, and the text between", () => {
    expect(markParts("Pro user, PROfile", "pro")).toEqual([
      { text: "Pro", hit: true },
      { text: " user, ", hit: false },
      { text: "PRO", hit: true },
      { text: "file", hit: false },
    ])
  })
  test("a text without it is one unmarked part", () => {
    expect(markParts("nothing", "x")).toEqual([{ text: "nothing", hit: false }])
    expect(markParts("", "x")).toEqual([{ text: "", hit: false }])
  })
  test("a % or an _ in the value is that character", () => {
    expect(markParts("50% off", "%")).toEqual([
      { text: "50", hit: false },
      { text: "%", hit: true },
      { text: " off", hit: false },
    ])
  })
})

describe("a cell as text", () => {
  test("NULL has no text and is never the empty string", () => {
    expect(cellText(null)).toBeNull()
    expect(cellText("")).toBe("")
  })
  test("a number keeps its digits, a document is its JSON", () => {
    expect(cellText("9007199254740993")).toBe("9007199254740993")
    expect(cellText(1.5)).toBe("1.5")
    expect(cellText(true)).toBe("true")
    expect(cellText({ a: [1] })).toBe('{"a":[1]}')
  })
  test("a long text is cut to a window around the match", () => {
    const long = `${"a".repeat(200)} needle ${"b".repeat(200)}`
    const cut = excerpt(long, "NEEDLE", 40)
    expect(cut).toContain("needle")
    expect(cut.startsWith("…")).toBe(true)
    expect(cut.endsWith("…")).toBe(true)
    expect(cut.length).toBeLessThanOrEqual(42)
    expect(excerpt("short", "x")).toBe("short")
  })
})

describe("the matches by table", () => {
  const result = {
    matches: [
      customer(1, "pro@example.com"),
      {
        schema: "public",
        table: "products",
        column: "name",
        value: "Pro kit",
        row: { id: "9", name: "Pro kit", sku: "P-1" },
      },
      customer(2, "ann@example.com", { tier: "pro" }),
    ],
    tablesScanned: 7,
    truncated: false,
  }
  const groups = groupHits(result, "pro")
  test("one group per table, in the order found, each with its rows", () => {
    expect(groups.map((group) => [group.table, group.hits.length])).toEqual([
      ["customers", 2],
      ["products", 1],
    ])
  })
  test("every cell that holds the value is marked, not only the first the server named", () => {
    const [first] = groups[0].hits
    expect(first.cells.map((cell) => cell.column)).toEqual(["email", "tier"])
    expect(first.cells[0].parts[0]).toEqual({ text: "pro", hit: true })
    expect(groups[0].columns).toEqual(["email", "tier"])
  })
  test("the other cells are a few short ones, the key first; matches and NULL are left out", () => {
    expect(otherCells(groups[0].hits[1], ["id"])).toEqual([
      { column: "id", text: "2" },
      { column: "email", text: "ann@example.com" },
      { column: "profile", text: '{"theme": "dark"}' },
    ])
    const wide = {
      match: {
        schema: "",
        table: "t",
        column: "z",
        value: "x",
        row: { a: "1", b: "2", c: "3", key: "k", z: "x" },
      },
      cells: [{ column: "z", parts: [] }],
    }
    expect(otherCells(wide, ["key"], 2)).toEqual([
      { column: "key", text: "k" },
      { column: "a", text: "1" },
    ])
    expect(otherCells(wide, undefined, 2).map((cell) => cell.column)).toEqual(["a", "b"])
  })
  test("a match the page cannot see in the row keeps the server's word for it", () => {
    const [group] = groupHits(
      {
        matches: [
          {
            schema: "",
            table: "t",
            column: "at",
            value: "2026-10-01 10:00:00",
            row: { at: "2026-10-01T10:00:00Z" },
          },
        ],
        tablesScanned: 1,
        truncated: false,
      },
      "10-01 10",
    )
    expect(group.hits[0].cells).toEqual([
      {
        column: "at",
        parts: [
          { text: "2026-", hit: false },
          { text: "10-01 10", hit: true },
          { text: ":00:00", hit: false },
        ],
      },
    ])
  })
})

describe("finding a matched row again", () => {
  test("by its key, when the table has one", () => {
    expect(rowFilters(customer(12, "a@b.c"), ["id"])).toEqual([
      { column: "id", op: "eq", value: "12" },
    ])
  })
  test("by every column of a composite key", () => {
    const match = {
      schema: "",
      table: "order_items",
      column: "sku",
      value: "x",
      row: { order_id: "7", line_no: "2", sku: "x" },
    }
    expect(rowFilters(match, ["order_id", "line_no"])).toEqual([
      { column: "order_id", op: "eq", value: "7" },
      { column: "line_no", op: "eq", value: "2" },
    ])
  })
  test("without a key: by the row's own short values, the matched cell first, NULL and documents left out", () => {
    expect(rowFilters(customer(12, "a@b.c", { ok: true }), [])).toEqual([
      { column: "email", op: "eq", value: "a@b.c" },
      { column: "id", op: "eq", value: "12" },
      { column: "tier", op: "eq", value: "pro" },
      { column: "ok", op: "eq", value: "1" },
    ])
    expect(rowFilters(customer(12, "a@b.c"), undefined)[0]).toEqual({
      column: "email",
      op: "eq",
      value: "a@b.c",
    })
  })
  test("a key whose value was cut for display is not a key to compare by", () => {
    const match = {
      schema: "",
      table: "blobs",
      column: "name",
      value: "n",
      row: { id: "\\x00ff… (9000 bytes)", name: "n" },
    }
    expect(rowFilters(match, ["id"])).toEqual([{ column: "name", op: "eq", value: "n" }])
  })
  test("never more conditions than one address carries", () => {
    const row = Object.fromEntries(Array.from({ length: 30 }, (_, i) => [`c${i}`, String(i)]))
    expect(
      rowFilters({ schema: "", table: "wide", column: "c0", value: "0", row }, []),
    ).toHaveLength(12)
  })
  test("every match of a table: the value in any column it was found in", () => {
    const [group] = groupHits(
      { matches: [customer(1, "pro@example.com")], tablesScanned: 1, truncated: false },
      "pro",
    )
    expect(tableFilters(group, "pro")).toEqual([
      { column: "email", op: "icontains", value: "pro" },
      { column: "tier", op: "icontains", value: "pro" },
    ])
  })
})

describe("what the server answered", () => {
  test("is read as a result whatever came back", () => {
    expect(readResult([])).toEqual({
      matches: [],
      tablesScanned: 0,
      tablesSkipped: [],
      truncated: false,
    })
    expect(
      readResult({ matches: [customer(1, "a")], tablesScanned: 3, truncated: true }).truncated,
    ).toBe(true)
    expect(readResult(null).matches).toEqual([])
  })
})
