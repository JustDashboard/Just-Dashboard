import { describe, expect, test } from "bun:test"
import { parseDocument } from "../bson"
import { cellFilter, tableModel } from "./table"

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
  })

  test("a null among typed values does not take the column's kind away", () => {
    const model = tableModel(listed('{"at":{"$date":{"$numberLong":"0"}}}', '{"at":null}'))
    expect(model.columns[0].kind).toBe("datetime")
    expect(model.rows[1][0]).toBeNull()
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
