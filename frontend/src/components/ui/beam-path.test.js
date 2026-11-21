import { expect, test } from "bun:test"
import { levelPath } from "./beam-path"

test("a line between two levels bends only across the gap", () => {
  // From a hub at 500 out to a mark at 100, the gap from 300 to 400.
  expect(levelPath({ x: 500, y: 80 }, { x: 100, y: 20 }, 300, 400)).toBe(
    "M 500,80 L 400,80 C 350,80 350,20 300,20 L 100,20",
  )
  // And the other way, to a mark on the far side.
  expect(levelPath({ x: 500, y: 80 }, { x: 900, y: 140 }, 600, 700)).toBe(
    "M 500,80 L 600,80 C 650,80 650,140 700,140 L 900,140",
  )
})

test("two ends on one level are joined by a straight line", () => {
  expect(levelPath({ x: 500, y: 80 }, { x: 100, y: 80 }, 300, 400)).toBe("M 500,80 L 100,80")
  // Within a pixel is one level: no bend too small to be meant.
  expect(levelPath({ x: 500, y: 80 }, { x: 100, y: 80.4 }, 300, 400)).toBe("M 500,80 L 100,80")
})

test("a gap that reaches past an end is cut there", () => {
  expect(levelPath({ x: 350, y: 80 }, { x: 100, y: 20 }, 300, 400)).toBe(
    "M 350,80 L 350,80 C 325,80 325,20 300,20 L 100,20",
  )
})
