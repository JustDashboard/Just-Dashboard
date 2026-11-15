import { expect, test } from "bun:test"
import { pickerGroups } from "./container-picker-model"

const ITEMS = [
  { name: "public", figure: "12" },
  { name: "pg_catalog", system: true },
  { name: "billing" },
  { name: "information_schema", system: true },
]

test("the containers somebody made come first, the engine's own after, each in the server's order", () => {
  const { own, system } = pickerGroups(ITEMS, "")
  expect(own.map((item) => item.name)).toEqual(["public", "billing"])
  expect(system.map((item) => item.name)).toEqual(["pg_catalog", "information_schema"])
})

test("the find box narrows both runs, ignoring case and the spaces around it", () => {
  const { own, system } = pickerGroups(ITEMS, "  PUB ")
  expect(own.map((item) => item.name)).toEqual(["public"])
  expect(system).toEqual([])
  expect(pickerGroups(ITEMS, "schema").system.map((item) => item.name)).toEqual([
    "information_schema",
  ])
})

test("nothing matches: both runs are empty, which the menu says in words", () => {
  const { own, system } = pickerGroups(ITEMS, "orders")
  expect(own.length + system.length).toBe(0)
})
