import { expect, test } from "bun:test"
import {
  CELL,
  beamOf,
  beyondInRow,
  carriedBeam,
  columnStart,
  fitColumn,
  flowTemplate,
  isLit,
  levelDrops,
  loadingRows,
  middleRows,
  nearerInRow,
  paintOrder,
  perRow,
  staggerOf,
  waitingLines,
} from "./wire-flow-model"

test("a line that is up pulses only while something moves on it", () => {
  expect(beamOf({ state: "up", moving: true })).toEqual({
    still: false,
    dashed: false,
    tone: "default",
  })
  expect(beamOf({ state: "up" })).toEqual({ still: true, dashed: false, tone: "default" })
  expect(beamOf(undefined)).toEqual({ still: true, dashed: false, tone: "default" })
})

test("a line that is down or idle is dashed and never moves", () => {
  expect(beamOf({ state: "down", moving: true })).toEqual({
    still: true,
    dashed: true,
    tone: "default",
  })
  expect(beamOf({ state: "idle", moving: true })).toEqual({
    still: true,
    dashed: true,
    tone: "default",
  })
})

test("amber and red keep their colour, moving or still", () => {
  expect(beamOf({ state: "warning", moving: true })).toEqual({
    still: false,
    dashed: false,
    tone: "warning",
  })
  expect(beamOf({ state: "danger" })).toEqual({ still: true, dashed: false, tone: "danger" })
  expect(beamOf({ state: "danger", dashed: true }).dashed).toBe(true)
})

test("lines start in fives", () => {
  expect([0, 1, 4, 5, 6].map(staggerOf)).toEqual([0, 0.3, 1.2, 0, 0.3])
})

test("a column draws what fits and counts the rest", () => {
  const nodes = [1, 2, 3, 4, 5, 6, 7]
  expect(fitColumn(nodes)).toEqual({ shown: nodes, hidden: 0 })
  expect(fitColumn(nodes, 3)).toEqual({ shown: [1, 2, 3], hidden: 4 })
  // One over is drawn rather than said: "1 more" would take the same row.
  expect(fitColumn([1, 2, 3, 4], 3)).toEqual({ shown: [1, 2, 3, 4], hidden: 0 })
  expect(fitColumn([1, 2], 3)).toEqual({ shown: [1, 2], hidden: 0 })
})

test("pointing at a node keeps it and what travels with it lit", () => {
  const related = (focus, id) => focus === "internet" && id === "docker:a"
  expect(isLit(null, "x")).toBe(true)
  expect(isLit("x", "x")).toBe(true)
  expect(isLit("x", "y")).toBe(false)
  expect(isLit("internet", "docker:a", related)).toBe(true)
  expect(isLit("internet", "docker:b", related)).toBe(false)
})

test("the wide grid is five columns, the sides sharing what the hub leaves", () => {
  expect(flowTemplate()).toBe(
    "minmax(0,1fr) clamp(3rem,6vw,6rem) 10rem clamp(3rem,6vw,6rem) minmax(0,1fr)",
  )
  expect(flowTemplate(1)).toContain(" 12.5rem ")
})

test("a side holds as many nodes to a row as it has room for", () => {
  const room = (nodes) => nodes * CELL.md + (nodes - 1) * CELL.gap
  expect(perRow(room(3), 3)).toBe(3)
  expect(perRow(room(3) - 1, 3)).toBe(2)
  expect(perRow(room(4), 8)).toBe(4)
  // One node is one row whatever the room, and a side with none still divides.
  expect(perRow(0, 1)).toBe(1)
  expect(perRow(0, 0)).toBe(1)
  expect(perRow(0, 5)).toBe(1)
})

test("the rows are evened out rather than filled", () => {
  const room = 4 * CELL.md + 3 * CELL.gap
  // Five are three and two, not four and one.
  expect(perRow(room, 5)).toBe(3)
  expect(perRow(room, 7)).toBe(4)
  expect(perRow(room, 9)).toBe(3)
  // Fewer than fit are one row.
  expect(perRow(room, 2)).toBe(2)
})

test("small marks stand closer, so more of them fit a row", () => {
  const room = 4 * CELL.sm + 3 * CELL.gap
  expect(perRow(room, 8, "sm")).toBe(4)
  expect(perRow(room, 8, "md")).toBe(3)
})

test("a short last row is pushed against the hub on the start side only", () => {
  // Eight in threes: the last row holds two, and starts in the second place.
  expect([0, 3, 6, 7].map((index) => columnStart(index, 8, 3, "start"))).toEqual([
    undefined,
    undefined,
    2,
    undefined,
  ])
  expect(columnStart(6, 8, 3, "end")).toBeUndefined()
  // A full last row starts where every row does.
  expect(columnStart(4, 8, 4, "start")).toBeUndefined()
  expect(columnStart(0, 2, 2, "start")).toBeUndefined()
})

test("the hub stands on the middle row, or between the two about the middle", () => {
  expect(middleRows(3, 3)).toEqual([0, 0])
  expect(middleRows(6, 3)).toEqual([0, 3])
  expect(middleRows(8, 3)).toEqual([3, 3])
  expect(middleRows(12, 3)).toEqual([3, 6])
  expect(middleRows(0, 3)).toEqual([0, 0])
})

test("each part is dropped to the lowest level among them", () => {
  expect(levelDrops({ start: 22, hub: 28, end: 22 })).toEqual({ start: 6, hub: 0, end: 6 })
  expect(levelDrops({ start: 150, hub: 28, end: 22 })).toEqual({ start: 0, hub: 122, end: 128 })
})

test("a node's line runs to the mark between it and the hub", () => {
  // Eight in threes on the start side, read towards the hub: 0 1 2 / 3 4 5 / 6 7.
  expect([0, 1, 2, 3, 6, 7].map((at) => nearerInRow(at, 8, 3, "start"))).toEqual([
    1,
    2,
    undefined,
    4,
    7,
    undefined,
  ])
  // The end side is read away from the hub: the first of a row is the nearest.
  expect([0, 1, 2, 3, 7].map((at) => nearerInRow(at, 8, 3, "end"))).toEqual([
    undefined,
    0,
    1,
    undefined,
    6,
  ])
  // A column a side: every line is its own way to the hub.
  expect(nearerInRow(2, 5, 1, "start")).toBeUndefined()
})

test("the rest of a row reaches the hub through the nodes nearer in", () => {
  expect(beyondInRow(2, 8, 3, "start")).toEqual([0, 1])
  expect(beyondInRow(0, 8, 3, "start")).toEqual([])
  expect(beyondInRow(3, 8, 3, "end")).toEqual([4, 5])
  expect(beyondInRow(7, 8, 3, "end")).toEqual([])
})

test("a switched-off node between working ones and the hub does not cut them off", () => {
  const off = beamOf({ state: "idle" })
  const up = beamOf({ state: "up" })
  const failing = beamOf({ state: "danger" })
  // Dashed only while nothing beyond it is drawn solid.
  expect(carriedBeam(off, []).dashed).toBe(true)
  expect(carriedBeam(off, [off]).dashed).toBe(true)
  expect(carriedBeam(off, [off, up]).dashed).toBe(false)
  // Its colour stays its own: a failing node beyond does not colour it.
  expect(carriedBeam(off, [failing])).toEqual({ still: true, dashed: false, tone: "default" })
  expect(carriedBeam(up, [failing])).toEqual(up)
})

test("the line that needs a look is painted over the ones that do not", () => {
  const order = [
    { state: "idle" },
    { state: "down" },
    { state: "up" },
    { state: "up", moving: true },
    { state: "warning" },
    { state: "danger" },
    undefined,
  ].map(paintOrder)
  expect(order).toEqual([0, 0, 1, 1, 2, 3, 1])
})

test("a loading column holds the rows it will have", () => {
  expect(loadingRows({})).toBe(3)
  expect(loadingRows({ max: 4 })).toBe(5)
  expect(loadingRows({ max: 4, loadingRows: 2 })).toBe(2)
  expect(loadingRows({ loadingRows: -1 })).toBe(0)
})

test("a waiting node with no shape holds one line of detail", () => {
  expect(waitingLines(undefined)).toEqual(["both"])
})

test("a count of waiting lines is that many at both widths", () => {
  expect(waitingLines(2)).toEqual(["both", "both"])
  expect(waitingLines(0)).toEqual([])
})

test("a waiting line only one width has comes after the shared ones", () => {
  expect(waitingLines({ wide: 1, narrow: 2 })).toEqual(["both", "narrow"])
  expect(waitingLines({ wide: 3, narrow: 1 })).toEqual(["both", "wide", "wide"])
})

test("a count of waiting lines that is not a count is none", () => {
  expect(waitingLines(-1)).toEqual([])
  expect(waitingLines(Number.NaN)).toEqual([])
  expect(waitingLines(1.9)).toEqual(["both"])
})
