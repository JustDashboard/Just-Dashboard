import { expect, test } from "bun:test"
import { RAIL_ROW, RAIL_ROW_PX, railSkeletonRows } from "./rail-model"

test("a rail row is 28px, and 36px below sm, in classes and in pixels alike", () => {
  expect(RAIL_ROW).toBe("h-7 max-sm:h-9")
  // h-7 is 1.75rem and h-9 2.25rem: the window of rows counts in the same pixels.
  expect(RAIL_ROW_PX).toEqual({ base: 28, phone: 36 })
})

test("the skeleton is a list: a head over five rows, then heads over two", () => {
  const rows = railSkeletonRows(3)
  expect(rows.map((row) => row.kind).join(" ")).toBe(
    "head row row row row row head row row head row row",
  )
  // Names of varied widths, never one block.
  const widths = new Set(rows.flatMap((row) => (row.kind === "row" ? [row.width] : [])))
  expect(widths.size).toBeGreaterThan(3)
})

test("a list with no groups is rows alone; two-line entries are fewer, as they are taller", () => {
  expect(railSkeletonRows(0).every((row) => row.kind === "row")).toBe(true)
  expect(railSkeletonRows(0)).toHaveLength(8)
  expect(railSkeletonRows(0, 2)).toHaveLength(6)
})
