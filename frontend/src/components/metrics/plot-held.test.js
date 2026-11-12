import { expect, test } from "bun:test"
import { plotHeld } from "./plot-held"

const rows = [{ ts: 1 }, { ts: 2 }]
const format = (v) => String(v)

test("near the view, the same props are the same plot and a new array of rows is not", () => {
  const drawn = { near: true, rows, format, unit: "%" }
  expect(plotHeld(drawn, { near: true, rows, format, unit: "%" })).toBe(true)
  expect(plotHeld(drawn, { near: true, rows: [...rows], format, unit: "%" })).toBe(false)
  expect(plotHeld(drawn, { near: true, rows, format: (v) => String(v), unit: "%" })).toBe(false)
})

test("a prop that appears or goes away is a change", () => {
  const drawn = { near: true, rows }
  expect(plotHeld(drawn, { near: true, rows, thresholds: [] })).toBe(false)
  expect(plotHeld({ near: true, rows, thresholds: [] }, drawn)).toBe(false)
})

test("out of reach, a plot holds whatever arrives", () => {
  const drawn = { near: true, rows }
  expect(plotHeld(drawn, { near: false, rows })).toBe(true)
  expect(plotHeld(drawn, { near: false, rows: [{ ts: 3 }] })).toBe(true)
})

test("back near the view, what arrived while it was away is drawn", () => {
  // memo compares against the props of the last draw, not of the last render skipped.
  const drawn = { near: true, rows }
  const later = [{ ts: 3 }]
  expect(plotHeld(drawn, { near: false, rows: later })).toBe(true)
  expect(plotHeld(drawn, { near: true, rows: later })).toBe(false)
})

test("back near the view with nothing new, nothing is drawn", () => {
  const drawn = { near: true, rows }
  expect(plotHeld(drawn, { near: false, rows })).toBe(true)
  expect(plotHeld(drawn, { near: true, rows })).toBe(true)
})
