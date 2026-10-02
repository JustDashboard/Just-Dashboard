import { describe, expect, test } from "bun:test"
import {
  PER_TABLE,
  cellKind,
  cellText,
  countText,
  excerpt,
  groupHits,
  isKeyed,
  markParts,
  otherCells,
  readResult,
  rowFilters,
  scanFilters,
  tableFilters,
  tableKey,
  tableMatches,
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
    // A document is not what a row is recognised by, and is left out with the NULL.
    expect(otherCells(groups[0].hits[1], ["id"])).toEqual([
      { column: "id", text: "2", kind: "number", key: true },
      { column: "email", text: "ann@example.com", kind: "text", key: false },
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
    expect(otherCells(wide, ["key"], 2).map((cell) => [cell.column, cell.key])).toEqual([
      ["key", true],
      ["a", false],
    ])
    // With no key known: a text before the numbers, each kind in the row's own order.
    expect(otherCells(wide, undefined, 2).map((cell) => cell.column)).toEqual(["key", "a"])
  })
  test("what a person reads comes before what a machine does: texts, numbers, instants, and a uuid only when there is nothing else", () => {
    const hit = {
      match: {
        schema: "public",
        table: "customers",
        column: "email",
        value: "a@b.c",
        row: {
          created_at: "2026-10-01T14:17:18.460246Z",
          external_id: "df8da1c2-2a3e-40b2-977f-25ac5f3edae0",
          opted_in: false,
          lifetime_value: "3201.79",
          full_name: "Ann Lee",
          email: "a@b.c",
          id: "1",
        },
      },
      cells: [{ column: "email", parts: [] }],
    }
    expect(otherCells(hit, ["id"]).map((cell) => [cell.column, cell.kind])).toEqual([
      ["id", "number"],
      ["full_name", "text"],
      ["lifetime_value", "number"],
      ["created_at", "instant"],
      ["opted_in", "flag"],
    ])
    const machine = {
      match: { ...hit.match, row: { email: "a@b.c", external_id: hit.match.row.external_id } },
      cells: hit.cells,
    }
    expect(otherCells(machine, undefined)).toEqual([
      { column: "external_id", text: hit.match.row.external_id, kind: "token", key: false },
    ])
    expect(cellKind("2026-10-01")).toBe("instant")
    expect(cellKind("\\x00ff… (9000 bytes)")).toBe("token")
    expect(cellKind(4)).toBe("number")
  })
  test("a table says whether it holds more than is shown: counted where it was, assumed at the cap where it was not", () => {
    const five = Array.from({ length: PER_TABLE }, (_, n) => customer(n, `u${n}@pro.io`))
    const [assumed] = groupHits({ matches: five, tablesScanned: 1, truncated: false }, "pro")
    expect(assumed.more).toBe(true)
    expect(countText(assumed)).toBe(`${PER_TABLE}+`)
    const [counted] = groupHits(
      {
        matches: five,
        tablesScanned: 1,
        truncated: false,
        more: { [tableKey("public", "customers")]: false },
      },
      "pro",
    )
    expect(counted.more).toBe(false)
    expect(countText(counted)).toBe(String(PER_TABLE))
    expect(countText(groups[1])).toBe("1")
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
  test("without a key: by the row's own short values — the matched cell, then texts, then numbers — NULL and documents left out", () => {
    expect(rowFilters(customer(12, "a@b.c", { ok: true }), [])).toEqual([
      { column: "email", op: "eq", value: "a@b.c" },
      { column: "tier", op: "eq", value: "pro" },
      { column: "id", op: "eq", value: "12" },
      { column: "ok", op: "eq", value: "1" },
    ])
    expect(isKeyed(customer(12, "a@b.c"), [])).toBe(false)
    expect(isKeyed(customer(12, "a@b.c"), undefined)).toBe(false)
    expect(isKeyed(customer(12, "a@b.c"), ["id"])).toBe(true)
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
  test("a moment is not a condition: each engine reads one written as text its own way", () => {
    // ClickHouse answered 400 "Cannot convert string '2026-10-01T00:00:13Z' to type DateTime".
    const view = {
      schema: "analytics",
      table: "page_views",
      column: "country",
      value: "US",
      row: {
        ts: "2026-10-01T00:00:13Z",
        user_id: "4233",
        path: "/p/33",
        country: "US",
        duration_ms: 986,
        day: "2026-10-01",
      },
    }
    expect(rowFilters(view, undefined)).toEqual([
      { column: "country", op: "eq", value: "US" },
      { column: "path", op: "eq", value: "/p/33" },
      { column: "user_id", op: "eq", value: "4233" },
      { column: "duration_ms", op: "eq", value: "986" },
      // A day alone is read the same everywhere.
      { column: "day", op: "eq", value: "2026-10-01" },
    ])
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

describe("a search of ticked tables", () => {
  test("asks for the value in any column, a dozen columns to a read", () => {
    const columns = Array.from({ length: 14 }, (_, n) => `c${n}`)
    const chunks = scanFilters(columns, "x")
    expect(chunks.map((chunk) => chunk.length)).toEqual([12, 2])
    expect(chunks[0][0]).toEqual({ column: "c0", op: "icontains", value: "x" })
    expect(scanFilters([], "x")).toEqual([])
  })
  test("is read into the same matches: the row by its columns, the first cell that holds the value named", () => {
    const read = tableMatches(
      "public",
      "customers",
      [
        {
          columns: ["id", "email", "note"],
          rows: [
            ["1", "ann@EXAMPLE.com", null],
            ["2", "bob@x.io", "see example.com"],
          ],
          truncated: false,
          primaryKey: ["id"],
        },
      ],
      "example.com",
    )
    expect(read.key).toEqual(["id"])
    expect(read.more).toBe(false)
    expect(read.matches.map((match) => [match.column, match.value, match.row.id])).toEqual([
      ["email", "ann@EXAMPLE.com", "1"],
      ["note", "see example.com", "2"],
    ])
    // NULL stays NULL in the row: it is never the empty string.
    expect(read.matches[0].row.note).toBeNull()
  })
  test("a row found by two reads is one row, and a table keeps only its share and says there were more", () => {
    const row = (n) => [String(n), `u${n}@example.com`]
    const first = { columns: ["id", "email"], rows: [row(1), row(2), row(3)], truncated: false }
    const second = { columns: ["id", "email"], rows: [row(2), row(3), row(4), row(5), row(6)] }
    const read = tableMatches("", "t", [first, second], "example")
    expect(read.matches.map((match) => match.row.id)).toEqual(["1", "2", "3", "4", "5"])
    expect(read.more).toBe(true)
    expect(tableMatches("", "t", [{ ...first, truncated: true }], "example").more).toBe(true)
    expect(tableMatches("", "t", [{}], "example")).toEqual({ matches: [], more: false, key: [] })
  })
})
