import { expect, test } from "bun:test"
import { revealDelta } from "./scroll-reveal"

const port = { top: 0, bottom: 800 }
const room = { up: 1000, down: 1000 }

test("a region already in view needs no move", () => {
  expect(revealDelta({ top: 100, bottom: 600 }, port, room)).toBe(0)
})

test("a region clipped at the bottom moves up just far enough", () => {
  expect(revealDelta({ top: 400, bottom: 1000 }, port, room)).toBe(212)
})

test("a region clipped at the top moves down just far enough", () => {
  expect(revealDelta({ top: -150, bottom: 450 }, port, room)).toBe(-162)
})

test("a region taller than the port shows its top", () => {
  expect(revealDelta({ top: 300, bottom: 1300 }, port, room)).toBe(288)
  expect(revealDelta({ top: -300, bottom: 900 }, port, room)).toBe(-312)
})

test("the move is clamped to the room the port has", () => {
  expect(revealDelta({ top: 400, bottom: 1000 }, port, { up: 0, down: 80 })).toBe(80)
  expect(revealDelta({ top: 400, bottom: 1000 }, port, { up: 0, down: 0 })).toBe(0)
})

test("applying the answer settles: asking again gives 0", () => {
  for (const region of [
    { top: 400, bottom: 1000 },
    { top: -150, bottom: 450 },
    { top: 300, bottom: 1300 },
    { top: 30, bottom: 790 },
  ]) {
    const delta = revealDelta(region, port, room)
    const moved = { top: region.top - delta, bottom: region.bottom - delta }
    expect(revealDelta(moved, port, room)).toBe(0)
  }
})
