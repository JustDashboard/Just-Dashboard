import { expect, test } from "bun:test"
import { RAIL_SIZE, WORK_BESIDE_SIDE, clamp, sideBeside, sideMax } from "./side-model"

// The frame at a window width, with the dashboard's sidebar open.
const frameAt = (window) => window - 248 - 24

test("one rule for every workbench: beside while the work keeps its floor", () => {
  expect(sideBeside(frameAt(1440), RAIL_SIZE.base, false)).toBe(true)
  // 900 wide: the frame is 628, and 628 - 256 is short of the work's 480.
  expect(sideBeside(frameAt(900), RAIL_SIZE.base, true)).toBe(false)
  expect(sideBeside(frameAt(1024), RAIL_SIZE.base, true)).toBe(true)
  // A phone's frame is the screen.
  expect(sideBeside(390, RAIL_SIZE.base, false)).toBe(false)
})

test("before the frame is measured the window answers", () => {
  expect(sideBeside(0, RAIL_SIZE.base, true)).toBe(true)
  expect(sideBeside(0, RAIL_SIZE.base, false)).toBe(false)
})

test("a wider column needs a wider frame, and is never dragged past the work's floor", () => {
  const frame = 900
  expect(sideBeside(frame, 420, true)).toBe(true)
  expect(sideBeside(frame, 421, true)).toBe(false)
  expect(sideMax(frame, RAIL_SIZE)).toBe(frame - WORK_BESIDE_SIDE)
  expect(sideMax(2000, RAIL_SIZE)).toBe(RAIL_SIZE.max)
  expect(sideMax(500, RAIL_SIZE)).toBe(RAIL_SIZE.min)
})

test("clamp holds a value to its range", () => {
  expect(clamp(5, 1, 3)).toBe(3)
  expect(clamp(-1, 1, 3)).toBe(1)
  expect(clamp(2, 1, 3)).toBe(2)
})
