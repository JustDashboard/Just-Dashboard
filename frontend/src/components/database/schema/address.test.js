import { describe, expect, test } from "bun:test"
import { objectKey, objectParams, readAddress, schemaParams, tableParams } from "./address"

const reader = (params) => (name) => params[name] ?? ""

describe("what the schema browser's address names", () => {
  test("a table is the section's own keys: schema and name together", () => {
    const address = readAddress({ schema: "sales", table: "orders" }, reader({ view: "keys" }))
    expect(address.selected).toEqual({ type: "table", schema: "sales", name: "orders" })
    expect(address.view).toBe("keys")
    expect(address.creating).toBeNull()
  })

  test("an object that holds no rows is its kind, its name and what tells namesakes apart", () => {
    const address = readAddress(
      { schema: "public", table: "" },
      reader({ object: "function", name: "armor", sig: "bytea, text[], text[]" }),
    )
    expect(address.selected).toEqual({
      type: "object",
      kind: "function",
      schema: "public",
      name: "armor",
      signature: "bytea, text[], text[]",
      table: "",
    })
  })

  test("a kind nobody has, or a view nobody has, is not believed", () => {
    expect(
      readAddress({ schema: "", table: "" }, reader({ object: "constructor", name: "x" })).selected,
    ).toBeNull()
    expect(readAddress({ schema: "", table: "t" }, reader({ view: "rows" })).view).toBe("columns")
    expect(readAddress({ schema: "", table: "" }, reader({ new: "database" })).creating).toBeNull()
  })

  test("?new=table asks for the new-table panel, whatever else is open", () => {
    expect(readAddress({ schema: "public", table: "" }, reader({ new: "table" })).creating).toBe(
      "table",
    )
    const over = readAddress({ schema: "public", table: "orders" }, reader({ new: "table" }))
    expect(over.creating).toBe("table")
    expect(over.selected.name).toBe("orders")
  })
})

describe("the addresses the tree links to", () => {
  test("opening a table leaves nothing of another object behind", () => {
    expect(tableParams("public", "orders")).toEqual({
      object: null,
      name: null,
      sig: null,
      on: null,
      view: null,
      new: null,
      schema: "public",
      table: "orders",
    })
  })

  test("a trigger carries the table it fires on; a routine its arguments", () => {
    const trigger = objectParams({
      kind: "trigger",
      schema: "public",
      name: "touch",
      table: "products",
    })
    expect(trigger).toMatchObject({ object: "trigger", name: "touch", on: "products", table: null })
    const overload = objectParams({
      kind: "function",
      schema: "public",
      name: "armor",
      signature: "bytea",
    })
    expect(overload).toMatchObject({ object: "function", sig: "bytea", on: null })
    // A sequence's `table` is the column it feeds, not something to tell it apart by.
    expect(
      objectParams({ kind: "sequence", schema: "public", name: "s", table: "orders.id" }).on,
    ).toBeNull()
  })

  test("a schema with nothing chosen names the schema alone", () => {
    expect(schemaParams("analytics")).toMatchObject({
      schema: "analytics",
      table: null,
      object: null,
    })
    expect(schemaParams("").schema).toBeNull()
  })

  test("two overloads, and two triggers of one name on two tables, are four rows", () => {
    const keys = new Set([
      objectKey({ kind: "function", schema: "public", name: "armor", signature: "bytea" }),
      objectKey({
        kind: "function",
        schema: "public",
        name: "armor",
        signature: "bytea, text[], text[]",
      }),
      objectKey({ kind: "trigger", schema: "public", name: "touch", table: "a" }),
      objectKey({ kind: "trigger", schema: "public", name: "touch", table: "b" }),
    ])
    expect(keys.size).toBe(4)
  })
})
