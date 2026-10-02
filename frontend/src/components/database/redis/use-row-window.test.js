import { describe, expect, test } from "bun:test"
import { visibleRows } from "./use-row-window"

describe("which rows a viewport shows", () => {
  test("the rows on screen, with a few past each edge", () => {
    expect(visibleRows(0, 280, 28, 1000)).toEqual({ start: 0, end: 22 })
    expect(visibleRows(2800, 280, 28, 1000)).toEqual({ start: 88, end: 122 })
  })

  test("never past the list's ends", () => {
    expect(visibleRows(27_900, 280, 28, 1000)).toEqual({ start: 984, end: 1000 })
    expect(visibleRows(-50, 280, 28, 5)).toEqual({ start: 0, end: 5 })
    expect(visibleRows(999_999, 280, 28, 5)).toEqual({ start: 5, end: 5 })
  })

  test("an empty list shows nothing", () => {
    expect(visibleRows(0, 280, 28, 0)).toEqual({ start: 0, end: 0 })
  })
})
