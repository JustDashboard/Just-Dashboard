import { expect, test } from "bun:test"
import { nameIndex, nextIndex } from "./keys"
import { heldRows } from "./held-list"
import { rangeSelection } from "./selection"

test("name navigation cycles matching names and retains multi-letter prefixes", () => {
  const names = ["nginx", "Node", "postgres", "node-exporter"]
  expect(nameIndex(names, "n", 0)).toBe(1)
  expect(nameIndex(names, "n", 3)).toBe(0)
  expect(nameIndex(names, "NODE", 0)).toBe(1)
  expect(nameIndex(names, "absent", -1)).toBe(-1)
  expect(nameIndex([], "n", -1)).toBe(-1)
})

test("arrows clamp at the edges and start at the appropriate edge without focus", () => {
  expect(nextIndex(3, -1, "ArrowDown")).toBe(0)
  expect(nextIndex(3, -1, "ArrowUp")).toBe(2)
  expect(nextIndex(3, 2, "ArrowDown")).toBe(2)
  expect(nextIndex(3, 0, "ArrowUp")).toBe(0)
  expect(nextIndex(3, 1, "Home")).toBe(0)
  expect(nextIndex(3, 1, "End")).toBe(2)
  expect(nextIndex(0, -1, "End")).toBe(-1)
  expect(nextIndex(3, 1, "Enter")).toBe(-1)
})

test("polling preserves row order, updates values and drops removed identities", () => {
  const rows = [
    { id: "new", count: 1 },
    { id: "b", count: 5 },
    { id: "a", count: 6 },
  ]
  expect(heldRows(["a", "b", "gone"], rows, (row) => row.id)).toEqual([rows[2], rows[1]])
})

test("ranges extend both directions without duplicates and never borrow a filtered-out anchor", () => {
  const names = ["a", "b", "c", "d"]
  expect(rangeSelection(names, ["b"], "b", "d")).toEqual(["b", "c", "d"])
  expect(rangeSelection(names, ["b"], "b", "a")).toEqual(["b", "a"])
  expect(rangeSelection(["c", "d"], ["b"], "b", "d")).toEqual(["b"])
})
