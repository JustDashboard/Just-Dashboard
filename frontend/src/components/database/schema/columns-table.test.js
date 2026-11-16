import { expect, test } from "bun:test"
import { COLUMNS_NARROW, columnDefault, columnMarks } from "./columns-model"

const DETAIL = {
  schema: "public",
  primaryKey: ["id"],
  foreignKeys: [
    {
      name: "orders_customer",
      columns: ["customer_id"],
      refTable: "customers",
      refColumns: ["id"],
    },
    {
      name: "orders_region",
      columns: ["region"],
      refSchema: "geo",
      refTable: "regions",
      refColumns: ["code"],
    },
  ],
  indexes: [
    { name: "orders_pkey", columns: ["id"], unique: true, primary: true },
    { name: "orders_ref", columns: ["ref"], unique: true },
    { name: "orders_pair", columns: ["a", "b"], unique: true },
    { name: "orders_live", columns: ["email"], unique: true, predicate: "deleted_at is null" },
  ],
}

test("the key is 'primary', or 'sorting key' where the engine's key only orders the rows", () => {
  expect(columnMarks(DETAIL, "primaryKey")({ name: "id" }).key).toBe("primary")
  expect(columnMarks(DETAIL, "none")({ name: "id" }).key).toBe("sorting key")
  expect(columnMarks(DETAIL, "primaryKey")({ name: "ref" }).key).toBeUndefined()
})

test("unique is a column's own unique index: not the primary key, not a pair, not a partial one", () => {
  const marks = columnMarks(DETAIL, "primaryKey")
  expect(marks({ name: "ref" }).unique).toBe(true)
  expect(marks({ name: "id" }).unique).toBe(false)
  expect(marks({ name: "a" }).unique).toBe(false)
  expect(marks({ name: "email" }).unique).toBe(false)
})

test("a foreign key names the table and column it points at, in its own schema or this one", () => {
  const marks = columnMarks(DETAIL, "primaryKey")
  expect(marks({ name: "customer_id" }).reference).toEqual({
    schema: "public",
    table: "customers",
    column: "id",
  })
  expect(marks({ name: "region" }).reference).toEqual({
    schema: "geo",
    table: "regions",
    column: "code",
  })
  expect(marks({ name: "total" }).reference).toBeUndefined()
})

test("the default is the written one, or the expression of a computed column", () => {
  expect(columnDefault({ default: "now()" })).toBe("now()")
  expect(columnDefault({ generated: "price * qty", default: "0" })).toBe("= price * qty")
  expect(columnDefault({})).toBe("")
})

test("the table reads down under 640px of its own width", () => {
  expect(COLUMNS_NARROW).toBe(640)
})
