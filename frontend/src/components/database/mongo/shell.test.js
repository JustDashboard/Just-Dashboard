import { describe, expect, test } from "bun:test"
import { isEmptyDocument, mergeFilter, positionOf, scan, shapeProblem, textOf } from "./shell"

describe("one whole value, in either spelling", () => {
  test("Extended JSON and the shell's spelling both read", () => {
    for (const text of [
      '{"a": 1}',
      "{ a: 1, 'b': \"two\", $or: [ { c: null }, { d: { $gt: 3 } } ], }",
      '{ _id: ObjectId("65f1c0ffee0123456789abcd"), at: ISODate("2024-05-01") }',
      "{ n: NumberLong(5), d: NumberDecimal('1.5'), t: Timestamp(1, 2), u: new Date(0) }",
      "{ name: /^a\\/b[/]c/i, other: RegExp('x', 'i') }",
      "{ a: 1 } // trailing comment",
      "/* leading */ { a: -1.5e3, b: .5, c: 0x1F, d: +3, e: -Infinity };",
      "[ { $match: {} }, { $limit: 5 } ]",
      '"a string"',
      "42",
      "{}",
    ]) {
      expect(scan(text)).toMatchObject({ ok: true })
    }
  })

  test("what is not one value is refused where it goes wrong", () => {
    const open = scan("{ age: { $gt: 60 }")
    expect(open).toMatchObject({ ok: false })
    expect(open.error.message).toContain("never closed")
    expect(open.error).toMatchObject({ line: 1, column: 1 })

    const missing = scan("{ age: }")
    expect(missing.error.message).toContain("where a value was expected")
    expect(missing.error).toMatchObject({ line: 1, column: 8 })

    const colon = scan("{\n  age 5\n}")
    expect(colon.error.message).toContain("':' was expected")
    expect(colon.error.line).toBe(2)

    expect(scan('{ a: "open }').error.message).toContain("string that is never closed")
    expect(scan("{ a: 1 } { b: 2 }").error.message).toContain("after the end of the value")
    expect(scan("{ a: 1 /* never").error.message).toContain("comment that is never closed")
    expect(scan("{ a: [1, 2 }").error.message).toContain("',' or ']'")
    expect(scan("").error.message).toContain("where a value was expected")
  })

  test("the fields of a document, with where each value sits", () => {
    const text = '{ $match: { status: "paid" }, "quoted key": [1, 2] }'
    const read = scan(text)
    expect(read.ok).toBe(true)
    expect(read.value.kind).toBe("document")
    expect(read.value.fields.map((field) => field.key)).toEqual(["$match", "quoted key"])
    expect(textOf(text, read.value.fields[0].value)).toBe('{ status: "paid" }')
    expect(textOf(text, read.value.fields[1].value)).toBe("[1, 2]")
  })

  test("a bracket inside a string, a pattern or a comment is not a bracket", () => {
    expect(scan('{ a: "}{][" }').ok).toBe(true)
    expect(scan("{ a: /}{/ }").ok).toBe(true)
    expect(scan("{ a: 1 /* } */ }").ok).toBe(true)
    expect(scan("{ a: '\\'}' }").ok).toBe(true)
  })

  test("a constructor's arguments belong to its value", () => {
    const text = '[ ObjectId("65f1c0ffee0123456789abcd"), new Date(0), Timestamp({ t: 1, i: 2 }) ]'
    const read = scan(text)
    expect(read.value.items.map((item) => textOf(text, item))).toEqual([
      'ObjectId("65f1c0ffee0123456789abcd")',
      "new Date(0)",
      "Timestamp({ t: 1, i: 2 })",
    ])
  })
})

describe("a field's text", () => {
  test("empty is none, and never wrong", () => {
    expect(shapeProblem("")).toBeNull()
    expect(shapeProblem("   \n")).toBeNull()
  })

  test("the shape a field takes", () => {
    expect(shapeProblem("{ a: 1 }", "document")).toBeNull()
    expect(shapeProblem("[1]", "document")).toBe("Write a document: { … }.")
    expect(shapeProblem("[{ $match: {} }]", "list")).toBeNull()
    expect(shapeProblem("{}", "list")).toBe("Write a list: [ … ].")
    expect(shapeProblem("5", "document or list")).toContain("a document { … } or a list")
    expect(shapeProblem("5", "value")).toBeNull()
  })

  test("a mistake is said with its line and column, as the server says its own", () => {
    expect(shapeProblem("{\n  a: \n}")).toBe(
      "Line 3, column 1: unexpected '}' where a value was expected.",
    )
  })
})

describe("a filter that matches everything", () => {
  test("no text, an empty document, a commented one", () => {
    expect(isEmptyDocument("")).toBe(true)
    expect(isEmptyDocument("{}")).toBe(true)
    expect(isEmptyDocument(" { /* nothing */ } ;")).toBe(true)
  })

  test("anything else is not: a field, a list, text that does not read", () => {
    expect(isEmptyDocument("{ a: 1 }")).toBe(false)
    expect(isEmptyDocument("[]")).toBe(false)
    expect(isEmptyDocument("{")).toBe(false)
  })
})

describe("a filter with one more condition", () => {
  const clause = '{ "status": "paid" }'

  test("on no filter, the condition is the filter", () => {
    expect(mergeFilter("", clause)).toBe(clause)
    expect(mergeFilter("{}", clause)).toBe(clause)
  })

  test("on a filter, the two are joined rather than one written into the other", () => {
    expect(mergeFilter("{ age: { $gt: 30 } }", clause)).toBe(
      '{ "$and": [{ age: { $gt: 30 } }, { "status": "paid" }] }',
    )
    // The filter already names the field: both conditions stand.
    expect(mergeFilter('{ status: "new" }', clause)).toBe(
      '{ "$and": [{ status: "new" }, { "status": "paid" }] }',
    )
  })

  test("a third condition joins the list, and one already there is not said twice", () => {
    const two = mergeFilter("{ a: 1 }", clause)
    expect(mergeFilter(two, '{ "b": 2 }')).toBe(
      '{ "$and": [{ a: 1 }, { "status": "paid" }, { "b": 2 }] }',
    )
    expect(mergeFilter(two, clause)).toBe(two)
    expect(mergeFilter(clause, clause)).toBe(clause)
  })
})

test("a place in the text, counted from one", () => {
  expect(positionOf("ab\ncd", 0)).toEqual({ line: 1, column: 1 })
  expect(positionOf("ab\ncd", 4)).toEqual({ line: 2, column: 2 })
})
