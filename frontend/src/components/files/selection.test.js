import { describe, expect, test } from "bun:test"
import { intersectingPaths, rangePaths, selectionRect } from "./selection"

describe("marquee selection", () => {
  const entries = [
    { path: "a", rect: { left: 10, top: 20, right: 50, bottom: 60 } },
    { path: "b", rect: { left: 60, top: 20, right: 100, bottom: 60 } },
    { path: "c", rect: { left: 10, top: 70, right: 50, bottom: 110 } },
  ]

  test("selects partial overlaps in either drag direction", () => {
    const start = { x: 30, y: 40 }
    const end = { x: 65, y: 65 }
    expect([...intersectingPaths(selectionRect(start, end), entries)]).toEqual(["a", "b"])
    expect([...intersectingPaths(selectionRect(end, start), entries)]).toEqual(["a", "b"])
  })

  test("shrinking a gesture removes its old hits but retains the additive snapshot", () => {
    const initial = new Set(["c"])
    expect([
      ...intersectingPaths(selectionRect({ x: 0, y: 0 }, { x: 90, y: 65 }), entries, initial),
    ]).toEqual(["c", "a", "b"])
    expect([
      ...intersectingPaths(selectionRect({ x: 0, y: 0 }, { x: 30, y: 40 }), entries, initial),
    ]).toEqual(["c", "a"])
    expect([...initial]).toEqual(["c"])
  })

  test("empty space and touching edges do not select entries", () => {
    expect(intersectingPaths({ left: 50, top: 20, right: 60, bottom: 60 }, entries).size).toBe(0)
    expect(intersectingPaths({ left: 0, top: 0, right: 0, bottom: 0 }, entries).size).toBe(0)
  })
})

test("ranges follow the visible order, reverse and tolerate a vanished anchor", () => {
  expect(rangePaths(["c", "a", "b"], "c", "b")).toEqual(["c", "a", "b"])
  expect(rangePaths(["c", "a", "b"], "b", "c")).toEqual(["c", "a", "b"])
  expect(rangePaths(["c", "a", "b"], "gone", "a")).toEqual(["a"])
})
