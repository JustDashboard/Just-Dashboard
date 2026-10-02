import { describe, expect, test } from "bun:test"
import {
  allRowsState,
  clampCell,
  clampSelection,
  collapse,
  EMPTY_SELECTION,
  extendTo,
  HEADER_ROW,
  isRange,
  moveCell,
  rangeContains,
  rangeOf,
  rangeSize,
  rowSpan,
  selectAll,
  selectCell,
  selectColumn,
  toggleAllRows,
  toggleRow,
  toggleRowRange,
} from "./selection"
import { describeAggregate, describeStatus, rangeAggregate } from "./status"

const BOUNDS = { rows: 100, cols: 8 }
const at = (row, col) => ({ row, col })

describe("moving the active cell", () => {
  test("arrows move one cell and stop at the edges", () => {
    expect(moveCell(at(5, 3), "up", BOUNDS)).toEqual(at(4, 3))
    expect(moveCell(at(5, 3), "down", BOUNDS)).toEqual(at(6, 3))
    expect(moveCell(at(5, 3), "left", BOUNDS)).toEqual(at(5, 2))
    expect(moveCell(at(5, 3), "right", BOUNDS)).toEqual(at(5, 4))
    expect(moveCell(at(99, 7), "down", BOUNDS)).toEqual(at(99, 7))
    expect(moveCell(at(99, 7), "right", BOUNDS)).toEqual(at(99, 7))
    expect(moveCell(at(0, 0), "left", BOUNDS)).toEqual(at(0, 0))
  })

  test("up from the first row lands on the header, and no further", () => {
    expect(moveCell(at(0, 3), "up", BOUNDS)).toEqual(at(HEADER_ROW, 3))
    expect(moveCell(at(HEADER_ROW, 3), "up", BOUNDS)).toEqual(at(HEADER_ROW, 3))
    expect(moveCell(at(HEADER_ROW, 3), "down", BOUNDS)).toEqual(at(0, 3))
  })

  test("Home and End run along the row; with Ctrl they run to the grid's corners", () => {
    expect(moveCell(at(5, 3), "rowStart", BOUNDS)).toEqual(at(5, 0))
    expect(moveCell(at(5, 3), "rowEnd", BOUNDS)).toEqual(at(5, 7))
    expect(moveCell(at(5, 3), "gridStart", BOUNDS)).toEqual(at(0, 0))
    expect(moveCell(at(5, 3), "gridEnd", BOUNDS)).toEqual(at(99, 7))
    expect(moveCell(at(5, 3), "columnStart", BOUNDS)).toEqual(at(0, 3))
    expect(moveCell(at(5, 3), "columnEnd", BOUNDS)).toEqual(at(99, 3))
  })

  test("a page is a screenful of rows, clamped, and never onto the header", () => {
    expect(moveCell(at(50, 2), "pageDown", BOUNDS, 20)).toEqual(at(70, 2))
    expect(moveCell(at(95, 2), "pageDown", BOUNDS, 20)).toEqual(at(99, 2))
    expect(moveCell(at(50, 2), "pageUp", BOUNDS, 20)).toEqual(at(30, 2))
    expect(moveCell(at(5, 2), "pageUp", BOUNDS, 20)).toEqual(at(0, 2))
    expect(moveCell(at(HEADER_ROW, 2), "pageDown", BOUNDS, 20)).toEqual(at(20, 2))
  })

  test("Tab runs along the row, wraps to the next, and leaves the grid past its last cell", () => {
    expect(moveCell(at(5, 3), "next", BOUNDS)).toEqual(at(5, 4))
    expect(moveCell(at(5, 7), "next", BOUNDS)).toEqual(at(6, 0))
    expect(moveCell(at(99, 7), "next", BOUNDS)).toBeNull()
    expect(moveCell(at(5, 3), "previous", BOUNDS)).toEqual(at(5, 2))
    expect(moveCell(at(5, 0), "previous", BOUNDS)).toEqual(at(4, 7))
    expect(moveCell(at(0, 0), "previous", BOUNDS)).toBeNull()
    expect(moveCell(at(HEADER_ROW, 7), "next", BOUNDS)).toEqual(at(0, 0))
  })

  test("a grid with no rows keeps the cell on the header; one with no columns has no cell", () => {
    const empty = { rows: 0, cols: 4 }
    expect(moveCell(at(HEADER_ROW, 1), "down", empty)).toEqual(at(HEADER_ROW, 1))
    expect(moveCell(at(HEADER_ROW, 1), "gridEnd", empty)).toEqual(at(HEADER_ROW, 3))
    expect(moveCell(at(0, 0), "right", { rows: 5, cols: 0 })).toBeNull()
    expect(clampCell(at(3, 3), { rows: 5, cols: 0 })).toBeNull()
  })
})

describe("ranges", () => {
  test("a range is the rectangle between its two corners, whichever way it was dragged", () => {
    const selection = { active: at(2, 6), anchor: at(8, 1), rows: [] }
    expect(rangeOf(selection)).toEqual({ top: 2, bottom: 8, left: 1, right: 6 })
    expect(rangeSize(rangeOf(selection))).toEqual({ cells: 42, rows: 7, cols: 6 })
    expect(isRange(selection)).toBe(true)
  })

  test("one active cell is a range of one, and no active cell is no range", () => {
    const single = selectCell(EMPTY_SELECTION, at(4, 4))
    expect(rangeOf(single)).toEqual({ top: 4, bottom: 4, left: 4, right: 4 })
    expect(isRange(single)).toBe(false)
    expect(rangeOf(EMPTY_SELECTION)).toBeNull()
    expect(rangeSize(null)).toEqual({ cells: 0, rows: 0, cols: 0 })
  })

  test("the header is never part of a range", () => {
    expect(rangeOf(selectCell(EMPTY_SELECTION, at(HEADER_ROW, 2)))).toBeNull()
    const fromHeader = extendTo(selectCell(EMPTY_SELECTION, at(HEADER_ROW, 2)), at(3, 2))
    expect(fromHeader).toEqual({ active: at(3, 2), anchor: null, rows: [] })
    const intoHeader = extendTo(selectCell(EMPTY_SELECTION, at(3, 2)), at(HEADER_ROW, 4))
    expect(rangeOf(intoHeader)).toEqual({ top: 0, bottom: 3, left: 2, right: 4 })
  })

  test("extending keeps the corner the range was started from", () => {
    let selection = selectCell(EMPTY_SELECTION, at(5, 5))
    selection = extendTo(selection, at(7, 6))
    selection = extendTo(selection, at(3, 2))
    expect(selection.anchor).toEqual(at(5, 5))
    expect(rangeOf(selection)).toEqual({ top: 3, bottom: 5, left: 2, right: 5 })
    expect(extendTo(selection, at(5, 5)).anchor).toBeNull()
  })

  test("containment and the per-row span agree with the rectangle", () => {
    const range = { top: 2, bottom: 4, left: 1, right: 3 }
    expect(rangeContains(range, 3, 2)).toBe(true)
    expect(rangeContains(range, 2, 1)).toBe(true)
    expect(rangeContains(range, 4, 3)).toBe(true)
    expect(rangeContains(range, 5, 2)).toBe(false)
    expect(rangeContains(range, 3, 4)).toBe(false)
    expect(rangeContains(null, 0, 0)).toBe(false)
    expect(rowSpan(range, 3)).toEqual([1, 3])
    expect(rowSpan(range, 1)).toEqual([-1, -1])
    expect(rowSpan(null, 3)).toEqual([-1, -1])
  })

  test("select all and a whole column are ranges like any other", () => {
    expect(rangeOf(selectAll(EMPTY_SELECTION, BOUNDS))).toEqual({
      top: 0,
      bottom: 99,
      left: 0,
      right: 7,
    })
    expect(selectAll(EMPTY_SELECTION, { rows: 0, cols: 3 })).toBe(EMPTY_SELECTION)
    expect(rangeOf(selectColumn(EMPTY_SELECTION, 3, BOUNDS))).toEqual({
      top: 0,
      bottom: 99,
      left: 3,
      right: 3,
    })
  })

  test("Escape collapses a range onto the active cell and leaves a single cell alone", () => {
    const range = { active: at(2, 2), anchor: at(0, 0), rows: ["a"] }
    expect(collapse(range)).toEqual({ active: at(2, 2), anchor: null, rows: ["a"] })
    const single = selectCell(EMPTY_SELECTION, at(1, 1))
    expect(collapse(single)).toBe(single)
  })

  test("a selection is pulled back inside a grid that shrank, and left alone when it fits", () => {
    const selection = { active: at(90, 7), anchor: at(10, 2), rows: ["a"] }
    expect(clampSelection(selection, { rows: 20, cols: 4 })).toEqual({
      active: at(19, 3),
      anchor: at(10, 2),
      rows: ["a"],
    })
    expect(clampSelection(selection, BOUNDS)).toBe(selection)
    expect(clampSelection(selection, { rows: 20, cols: 0 }).active).toBeNull()
  })
})

describe("the selector column", () => {
  const IDS = ["a", "b", "c", "d", "e"]

  test("a click toggles one row", () => {
    expect(toggleRow([], "b")).toEqual(["b"])
    expect(toggleRow(["a", "b"], "a")).toEqual(["b"])
  })

  test("a shift-click gives every row in between the state of the one clicked", () => {
    expect(toggleRowRange(["a"], IDS, 0, 3, true)).toEqual(["a", "b", "c", "d"])
    expect(toggleRowRange(["a", "b", "c", "d"], IDS, 3, 1, false)).toEqual(["a"])
    expect(toggleRowRange([], IDS, 4, 2, true)).toEqual(["c", "d", "e"])
    expect(toggleRowRange([], IDS, -3, 99, true)).toEqual(IDS)
  })

  test("the header box selects the page, clears it, and reports a part-selection", () => {
    expect(toggleAllRows([], IDS)).toEqual(IDS)
    expect(toggleAllRows(["b"], IDS)).toEqual(["b", "a", "c", "d", "e"])
    expect(toggleAllRows([...IDS, "elsewhere"], IDS)).toEqual(["elsewhere"])
    expect(toggleAllRows(["x"], [])).toEqual([])
    expect(allRowsState([], IDS)).toBe(false)
    expect(allRowsState(["b"], IDS)).toBe("mixed")
    expect(allRowsState(IDS, IDS)).toBe(true)
    expect(allRowsState(["elsewhere"], IDS)).toBe(false)
  })
})

describe("the status line", () => {
  const kinds = ["text", "number", "number", "boolean"]
  const rows = [
    ["a", "9007199254740993", "1.50", true],
    ["b", "9007199254740993", null, false],
    ["c", "4", "2.25", true],
  ]
  const valueAt = (row, col) => rows[row][col]

  test("figures cover the numeric columns of the range and skip NULL", () => {
    const range = { top: 0, bottom: 2, left: 0, right: 3 }
    expect(rangeAggregate(range, kinds, valueAt)).toMatchObject({
      count: 5,
      sum: "18014398509481993.75",
      min: "1.5",
      max: "9007199254740993",
      exact: true,
    })
  })

  test("a range without two numbers in it has no figures", () => {
    expect(rangeAggregate({ top: 0, bottom: 2, left: 0, right: 0 }, kinds, valueAt)).toBeNull()
    expect(rangeAggregate({ top: 1, bottom: 1, left: 1, right: 2 }, kinds, valueAt)).toBeNull()
    expect(rangeAggregate({ top: 0, bottom: 0, left: 1, right: 1 }, kinds, valueAt)).toBeNull()
    expect(rangeAggregate(null, kinds, valueAt)).toBeNull()
  })

  test("the words say what is shown and what is selected, with the right plurals", () => {
    const base = { rows: 300, totalRows: 300, selectedRows: 0, selectedCells: 1, aggregate: null }
    expect(describeStatus(base)).toEqual(["300 rows"])
    expect(describeStatus({ ...base, rows: 1, totalRows: 1 })).toEqual(["1 row"])
    expect(describeStatus({ ...base, rows: 12, totalRows: 9000 })).toEqual(["12 of 9,000 rows"])
    expect(describeStatus({ ...base, selectedRows: 1, selectedCells: 24 })).toEqual([
      "300 rows",
      "1 row selected",
      "24 cells selected",
    ])
  })

  test("the figures print in a fixed order with grouped digits", () => {
    const figures = describeAggregate({
      count: 2,
      sum: "1234567.5",
      avg: "617283.75",
      min: "7",
      max: "1234560.5",
      exact: true,
    })
    expect(figures).toEqual([
      ["Sum", "1,234,567.5"],
      ["Avg", "617,283.75"],
      ["Min", "7"],
      ["Max", "1,234,560.5"],
    ])
  })
})
