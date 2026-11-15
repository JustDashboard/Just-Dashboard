import { expect, test } from "bun:test"
import {
  beamOf,
  fitColumn,
  flowTemplate,
  isLit,
  loadingRows,
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

test("the wide grid is five columns with the hub's share in the middle", () => {
  expect(flowTemplate()).toBe(
    "minmax(0,1fr) clamp(2.5rem,6vw,6rem) minmax(0,0.8fr) clamp(2.5rem,6vw,6rem) minmax(0,1fr)",
  )
  expect(flowTemplate(0.9)).toContain("minmax(0,0.9fr)")
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
