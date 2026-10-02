import { describe, expect, test } from "bun:test"
import { parseDocument } from "../bson"
import { cellFilter, sortOf, sortText, tableModel } from "./table"

const listed = (...canonicals) =>
  canonicals.map((canonical) => ({
    doc: { id: "", canonical, relaxed: {}, size: 0, digest: "" },
    root: parseDocument(canonical),
  }))

describe("documents as rows", () => {
  test("the columns are the union of the top-level fields, in the order first met, _id first", () => {
    const model = tableModel(
      listed(
        '{"name":"a","_id":{"$numberInt":"1"},"age":{"$numberInt":"30"}}',
        '{"_id":{"$numberInt":"2"},"city":"Iasi","name":"b"}',
      ),
    )
    expect(model.columns.map((column) => column.key)).toEqual(["_id", "name", "age", "city"])
    expect(model.rows).toEqual([
      ["1", "a", "30", null],
      ["2", "b", null, "Iasi"],
    ])
  })

  test("a column takes a kind when every value has it, and is plain where they differ", () => {
    const model = tableModel(
      listed(
        '{"n":{"$numberLong":"9007199254740993"},"mixed":"text","when":{"$date":{"$numberLong":"0"}},"sub":{"a":true}}',
        '{"n":{"$numberDecimal":"1.50"},"mixed":{"$numberInt":"5"},"when":{"$date":{"$numberLong":"1000"}},"sub":{"b":null}}',
      ),
    )
    const kinds = Object.fromEntries(model.columns.map((column) => [column.key, column.kind]))
    expect(kinds).toEqual({ n: "number", mixed: "unknown", when: "datetime", sub: "json" })
    // A 64-bit integer is its digits, not a rounded number.
    expect(model.rows[0][0]).toBe("9007199254740993")
    expect(model.rows[1][0]).toBe("1.50")
    expect(model.rows.map((row) => row[1])).toEqual(['"text"', "5"])
  })

  test("a column of more than one type spells each value with its type", () => {
    const model = tableModel(
      listed(
        '{"_id":{"$oid":"6abe79972945ac11a3124bfc"}}',
        '{"_id":"6abe79972945ac11a3124bfc"}',
        '{"_id":{"$numberInt":"7"}}',
      ),
    )
    expect(model.columns[0]).toMatchObject({ kind: "unknown", typeName: "mixed" })
    expect(model.rows.map((row) => row[0])).toEqual([
      'ObjectId("6abe79972945ac11a3124bfc")',
      '"6abe79972945ac11a3124bfc"',
      "7",
    ])
    // The filter a cell asks for is still written with the value's own type.
    expect(
      cellFilter(
        { column: model.columns[0], op: "eq", value: '"6abe79972945ac11a3124bfc"' },
        model,
      ),
    ).toBe('{ "_id": "6abe79972945ac11a3124bfc" }')
  })

  test("a column's head says its type, and how many documents have the field", () => {
    const model = tableModel(
      listed(
        '{"_id":{"$numberInt":"1"},"name":"a","legacy":{"$numberLong":"5"}}',
        '{"_id":{"$numberInt":"2"},"name":null}',
        '{"_id":{"$numberInt":"3"},"name":"c"}',
      ),
    )
    const heads = Object.fromEntries(model.columns.map((column) => [column.key, column.typeName]))
    expect(heads).toEqual({ _id: "Int32", name: "String", legacy: "Int64 · 1 of 3" })
    // Two cells are NULL for want of the field; the stored null is not one of them.
    expect(model.absent).toBe(2)
  })

  test("a null among typed values does not take the column's kind away", () => {
    const model = tableModel(listed('{"at":{"$date":{"$numberLong":"0"}}}', '{"at":null}'))
    expect(model.columns[0].kind).toBe("datetime")
    expect(model.rows[1][0]).toBeNull()
  })
})

describe("the order the heads ask for", () => {
  test("is written as the query bar's Sort", () => {
    expect(sortText([])).toBe("")
    expect(
      sortText([
        { column: "age", desc: true },
        { column: "name", desc: false },
      ]),
    ).toBe('{ "age": -1, "name": 1 }')
  })

  test("a column a sort cannot name gives none", () => {
    expect(sortText([{ column: "a.b", desc: false }])).toBeNull()
    expect(sortText([{ column: "$x", desc: false }])).toBeNull()
  })

  test("and the Sort is read back onto the heads, in either spelling", () => {
    expect(sortOf('{ "age": -1, "name": 1 }')).toEqual([
      { column: "age", desc: true },
      { column: "name", desc: false },
    ])
    expect(sortOf("{ age: 1 }")).toEqual([{ column: "age", desc: false }])
    expect(sortOf("")).toEqual([])
  })

  test("a sort that is not fields going up or down marks no head", () => {
    expect(sortOf('{ score: { $meta: "textScore" } }')).toEqual([])
    expect(sortOf("{ age: 5 }")).toEqual([])
    expect(sortOf("{ age: ")).toEqual([])
  })
})

describe("a filter from a cell", () => {
  const model = tableModel(
    listed(
      '{"_id":{"$oid":"6abe79972945ac11a3124bfc"},"n":{"$numberLong":"3"},"name":"a","a.b":{"$numberInt":"1"}}',
    ),
  )
  const column = (key) => model.columns.find((entry) => entry.key === key)

  test("the value is written with the type it has in the document", () => {
    expect(
      cellFilter({ column: column("_id"), op: "eq", value: "6abe79972945ac11a3124bfc" }, model),
    ).toBe('{ "_id": {"$oid":"6abe79972945ac11a3124bfc"} }')
    expect(cellFilter({ column: column("n"), op: "eq", value: "3" }, model)).toBe(
      '{ "n": {"$numberLong":"3"} }',
    )
    expect(cellFilter({ column: column("name"), op: "ne", value: "a" }, model)).toBe(
      '{ "name": { "$ne": "a" } }',
    )
  })

  test("the two null tests", () => {
    expect(cellFilter({ column: column("name"), op: "is_null" }, model)).toBe('{ "name": null }')
    expect(cellFilter({ column: column("name"), op: "not_null" }, model)).toBe(
      '{ "name": { "$ne": null } }',
    )
  })

  test("a field a filter cannot name, and a value the page does not hold, give no clause", () => {
    expect(cellFilter({ column: column("a.b"), op: "eq", value: "1" }, model)).toBeNull()
    expect(cellFilter({ column: column("name"), op: "eq", value: "zzz" }, model)).toBeNull()
  })
})
