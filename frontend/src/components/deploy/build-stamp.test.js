import { describe, expect, test } from "bun:test"
import { elapsed, stampColumnCh } from "./build-stamp"

const from = Date.parse("2026-11-15T10:00:00Z")
const at = (seconds) => new Date(from + seconds * 1000).toISOString()

describe("elapsed", () => {
  test("minutes and seconds inside the first hour, hours after it", () => {
    expect(elapsed(at(64), from)).toBe("+1:04")
    expect(elapsed(at(3729), from)).toBe("+1:02:09")
    expect(elapsed(at(-5), from)).toBe("+0:00")
  })

  test("a line with no readable time has no stamp", () => {
    expect(elapsed("not a time", from)).toBe("")
  })
})

describe("stampColumnCh", () => {
  test("the column is as wide as the longest stamp, so a stamp past an hour never overprints", () => {
    const short = [at(4), at(64)].map((ts) => elapsed(ts, from))
    expect(stampColumnCh(short)).toBe(6)
    const long = [...short, elapsed(at(3729), from)]
    expect(stampColumnCh(long)).toBe("+1:02:09".length)
    expect(stampColumnCh([elapsed(at(36_000 + 59), from)])).toBe("+10:00:59".length)
  })

  test("an empty transcript keeps the short column", () => {
    expect(stampColumnCh([])).toBe(6)
  })
})
