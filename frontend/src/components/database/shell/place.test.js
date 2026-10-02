import { describe, expect, test } from "bun:test"
import { namedPlace, rememberedPlace, restoredQuery, saidPlace, writtenQuery } from "./place"

const DATA = ["schema", "table"]
const QUERY = ["schema"]
const NONE = []

const said = (query, carries) => saidPlace(new URLSearchParams(query), carries)

describe("what an address says about the place", () => {
  test("naming any of the page's keys speaks for all of them", () => {
    expect(said("schema=public&table=orders", DATA)).toEqual({ schema: "public", table: "orders" })
    // A schema with no table is "no table is open", not "nothing said".
    expect(said("schema=public", DATA)).toEqual({ schema: "public", table: "" })
  })

  test("a page speaks only for what it can hold", () => {
    expect(said("schema=public&table=orders", QUERY)).toEqual({ schema: "public" })
    expect(said("schema=public&table=orders", NONE)).toBeNull()
  })

  test("a bare address says nothing", () => {
    expect(said("", DATA)).toBeNull()
    expect(said("view=queries&sql=select+1", DATA)).toBeNull()
  })
})

describe("what is remembered after an address has spoken", () => {
  test("a named key takes its value and an empty one is forgotten", () => {
    expect(rememberedPlace({}, { schema: "public", table: "orders" })).toEqual({
      schema: "public",
      table: "orders",
    })
    expect(rememberedPlace({ schema: "public", table: "orders" }, { table: "" })).toEqual({
      schema: "public",
    })
  })

  test("a page that cannot hold the table leaves it remembered", () => {
    const held = { schema: "public", table: "orders" }
    expect(rememberedPlace(held, { schema: "public" })).toBe(held)
    expect(rememberedPlace(held, {})).toBe(held)
  })

  test("a table does not follow the reader into another schema", () => {
    expect(rememberedPlace({ schema: "public", table: "orders" }, { schema: "sales" })).toEqual({
      schema: "sales",
    })
    expect(rememberedPlace({ db: "app", collection: "users" }, { db: "logs" })).toEqual({
      db: "logs",
    })
    // Named together, the new table is the new schema's.
    expect(
      rememberedPlace({ schema: "public", table: "orders" }, { schema: "sales", table: "leads" }),
    ).toEqual({ schema: "sales", table: "leads" })
  })

  test("nothing changed is the same object, so there is nothing to write", () => {
    const held = { schema: "public", table: "orders" }
    expect(rememberedPlace(held, { schema: "public", table: "orders" })).toBe(held)
    expect(rememberedPlace(held, { table: "users" })).not.toBe(held)
  })
})

describe("a round trip through a page that cannot hold the table", () => {
  test("Data, Settings, Data keeps the table", () => {
    let held = rememberedPlace({}, said("schema=public&table=orders", DATA))
    // Settings: the address says nothing and the memory stands.
    held = rememberedPlace(held, said("", NONE) ?? {})
    expect(restoredQuery("", DATA, held)).toBe("schema=public&table=orders")
  })

  test("Data, Query, Data keeps the table while the schema stays", () => {
    let held = rememberedPlace({}, said("schema=public&table=orders", DATA))
    held = rememberedPlace(held, said("schema=public", QUERY))
    expect(held).toEqual({ schema: "public", table: "orders" })
    held = rememberedPlace(held, said("schema=sales", QUERY))
    expect(restoredQuery("", DATA, held)).toBe("schema=sales")
  })
})

describe("a bare address", () => {
  const held = { schema: "public", table: "orders" }

  test("is completed with what the page can hold, keeping its own keys", () => {
    expect(restoredQuery("", QUERY, held)).toBe("schema=public")
    expect(restoredQuery("sql=select+1", QUERY, held)).toBe("sql=select+1&schema=public")
  })

  test("is left alone when it speaks for itself or nothing is remembered", () => {
    expect(restoredQuery("schema=sales", DATA, held)).toBeNull()
    expect(restoredQuery("", DATA, {})).toBeNull()
    expect(restoredQuery("", NONE, held)).toBeNull()
  })
})

describe("a write to the address", () => {
  test("sets what is named, removes what is cleared and keeps the rest", () => {
    expect(writtenQuery("schema=public&table=orders&view=rows", { table: "users" })).toBe(
      "schema=public&table=users&view=rows",
    )
    expect(writtenQuery("schema=public&table=orders", { table: null })).toBe("schema=public")
    expect(writtenQuery("sql=select+1", { sql: undefined })).toBe("")
  })

  test("two writes in a row both land", () => {
    const first = writtenQuery("schema=public", { table: "orders" })
    expect(writtenQuery(first, { view: "rows" })).toBe("schema=public&table=orders&view=rows")
  })

  test("only the place's own keys are remembered from a write", () => {
    expect(namedPlace({ table: "orders", view: "rows", key: "user:1", sql: "select 1" })).toEqual({
      table: "orders",
    })
    expect(namedPlace({ schema: null, table: undefined })).toEqual({ schema: "", table: "" })
  })
})
