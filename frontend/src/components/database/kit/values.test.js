import { describe, expect, test } from "bun:test"
import { binarySize, jsonPath, sizeWord, valueKind, valuePreview } from "./values"

describe("what kind of thing a value is", () => {
  test("NULL is not the empty string", () => {
    expect(valueKind(null)).toBe("null")
    expect(valueKind(undefined)).toBe("null")
    expect(valueKind("")).toBe("empty")
    expect(valuePreview(null, "null").text).toBe("NULL")
    expect(valuePreview("", "empty").text).toBe("empty")
  })

  test("numbers, including the ones that arrive as strings to keep their digits", () => {
    expect(valueKind(3.5)).toBe("number")
    expect(valueKind("9007199254740993", "bigint")).toBe("number")
    expect(valueKind("12.50", "numeric(10,2)")).toBe("number")
    expect(valueKind("NaN", "double precision")).toBe("number")
    // The same digits in a text column are text.
    expect(valueKind("9007199254740993", "text")).toBe("string")
    expect(valueKind("1 day", "interval")).toBe("string")
  })

  test("booleans, moments, JSON and bytes", () => {
    expect(valueKind(true)).toBe("boolean")
    expect(valueKind("2026-10-01T08:20:06.123Z")).toBe("date")
    expect(valueKind("2026-10-01", "date")).toBe("date")
    expect(valueKind("08:20:06", "time")).toBe("date")
    expect(valueKind({ a: 1 })).toBe("json")
    expect(valueKind([1, 2])).toBe("json")
    expect(valueKind('{"a":1}', "jsonb")).toBe("json")
    expect(valueKind("\\x1f8b0800")).toBe("binary")
    expect(valueKind("\\x" + "ab".repeat(256) + "… (4096 bytes)")).toBe("binary")
    expect(valueKind("\\xnot hex")).toBe("string")
  })

  test("the column's type decides what a string is", () => {
    // Four characters in a text column, a byte in a bytes column.
    expect(valueKind("\\x41", "text")).toBe("string")
    expect(valueKind("\\x41", "character varying(255)")).toBe("string")
    expect(valueKind("\\x41", "bytea")).toBe("binary")
    expect(valueKind("\\x41", "BLOB")).toBe("binary")
    expect(valueKind("\\x41", "binary")).toBe("binary")
    expect(valueKind("\\x41", "RAW(16)")).toBe("binary")
    expect(valueKind("NaN", "BINARY_DOUBLE")).toBe("number")
    // A note that opens with a date is a note.
    expect(valueKind("2024-01-01 10:00 call the bank", "text")).toBe("string")
    expect(valueKind("2024-01-01 10:00:00+00", "timestamptz")).toBe("date")
    // MySQL's YEAR is a whole number between two bounds, which is how the
    // grid edits it and so how it is read everywhere.
    expect(valueKind("2024", "year")).toBe("number")
    // MySQL's boolean arrives as 0 or 1.
    expect(valueKind("1", "tinyint(1)")).toBe("number")
    // A range is its bounds as the engine prints them, not a number or a date.
    expect(valueKind("[1,10)", "int4range")).toBe("string")
    expect(valueKind("101", "bit(3)")).toBe("string")
    // A boolean some drivers hand over as its word.
    expect(valueKind("true", "boolean")).toBe("boolean")
    expect(valueKind("f", "bool")).toBe("boolean")
    expect(valueKind("true", "text")).toBe("string")
    // The server's own words for a column work the same as the engine's.
    expect(valueKind("42", "integer")).toBe("number")
    expect(valueKind("42", "uuid")).toBe("string")
    expect(valueKind("2026-10-01T08:20:06Z", "datetime")).toBe("date")
    expect(valueKind("{1,2}", "integer[]")).toBe("string")
  })

  test("with no type, only what cannot be mistaken is claimed", () => {
    expect(valueKind("2024-01-01 10:00 call the bank")).toBe("string")
    expect(valueKind("2024-01-01 10:00")).toBe("date")
    expect(valueKind("2026-10-01T08:20:06+02:00")).toBe("date")
    expect(valueKind("2026-10-01")).toBe("string")
    expect(valueKind("0123")).toBe("string")
    expect(valueKind("true")).toBe("string")
  })
})

describe("how a value reads on one line", () => {
  test("a short value is itself", () => {
    expect(valuePreview("hello", "string")).toEqual({ text: "hello", clipped: false })
    expect(valuePreview(42, "number")).toEqual({ text: "42", clipped: false })
    expect(valuePreview(false, "boolean")).toEqual({ text: "false", clipped: false })
  })

  test("long text is cut and says so, and a line break does not break the row", () => {
    const long = "x".repeat(500)
    const preview = valuePreview(long, "string", 20)
    expect(preview).toEqual({ text: `${"x".repeat(20)}…`, clipped: true })
    expect(valuePreview("a\nb\r\nc", "string").text).toBe("a↵b↵c")
  })

  test("JSON is drawn compact", () => {
    expect(valuePreview({ a: [1, 2], b: null }, "json").text).toBe('{"a":[1,2],"b":null}')
    expect(valuePreview('{"a": 1}', "json").text).toBe('{"a": 1}')
  })

  test("bytes are their first eight and their size", () => {
    expect(valuePreview("\\x1f8b0800", "binary")).toEqual({ text: "0x1f8b0800", clipped: false })
    expect(binarySize("\\x1f8b0800")).toBe(4)
    const cut = "\\x" + "ab".repeat(256) + "… (4096 bytes)"
    expect(valuePreview(cut, "binary")).toEqual({ text: `0x${"ab".repeat(8)}`, clipped: true })
    expect(binarySize(cut)).toBe(4096)
    expect(binarySize("plain")).toBe(0)
  })
})

describe("naming a place in a document", () => {
  test("keys follow a dot, indexes take brackets, and an awkward key is quoted", () => {
    expect(jsonPath([])).toBe("$")
    expect(jsonPath(["user", "addresses", 0, "city"])).toBe("$.user.addresses[0].city")
    expect(jsonPath(["postal code"])).toBe('$["postal code"]')
    expect(jsonPath(["a.b", 2, "$oid"])).toBe('$["a.b"][2].$oid')
  })

  test("a container says how much it holds", () => {
    expect(sizeWord({ a: 1, b: 2, c: 3 })).toBe("3 keys")
    expect(sizeWord([1])).toBe("1 item")
    expect(sizeWord([])).toBe("0 items")
  })
})
