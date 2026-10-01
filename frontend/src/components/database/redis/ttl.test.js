import { describe, expect, test } from "bun:test"
import {
  LATEST_EXPIRY_MS,
  entryMoment,
  localMoment,
  parseMoment,
  parseTtl,
  remainingMs,
  ttlWord,
} from "./ttl"

const NOW = Date.UTC(2026, 9, 1, 12, 0, 0)

describe("a span as typed", () => {
  test("seconds, and parts with a unit", () => {
    expect(parseTtl("90", NOW)).toEqual({ ok: true, seconds: 90 })
    expect(parseTtl("60s", NOW)).toEqual({ ok: true, seconds: 60 })
    expect(parseTtl("5m", NOW)).toEqual({ ok: true, seconds: 300 })
    expect(parseTtl("2h", NOW)).toEqual({ ok: true, seconds: 7200 })
    expect(parseTtl("7d", NOW)).toEqual({ ok: true, seconds: 604_800 })
    expect(parseTtl("1w", NOW)).toEqual({ ok: true, seconds: 604_800 })
    expect(parseTtl(" 1H 30m ", NOW)).toEqual({ ok: true, seconds: 5400 })
  })

  // The old box sent `Number(text) || -1`, and a negative expiry means "never
  // expire": each of these cleared the key's expiry and said so.
  test("what the old box read as 'no expiry' is refused, never sent", () => {
    for (const typed of ["", "   ", "0", "soon", "60 sec", "-5", "1.5", "5x", "abc60"]) {
      const result = parseTtl(typed, NOW)
      expect(result.ok).toBe(false)
      expect(result.why.length).toBeGreaterThan(0)
    }
  })

  test("zero made of units is still zero", () => {
    expect(parseTtl("0m", NOW).ok).toBe(false)
  })

  test("a span that ends past what the server will set is refused", () => {
    const tooFar = String(Math.floor((LATEST_EXPIRY_MS - NOW) / 1000) + 1)
    expect(parseTtl(tooFar, NOW).ok).toBe(false)
    // The year 9999 is within it.
    expect(parseTtl(String(Math.floor((253_402_300_800_000 - NOW) / 1000)), NOW).ok).toBe(true)
    expect(parseTtl("99999999999999999999", NOW).ok).toBe(false)
  })
})

describe("a moment from a date field", () => {
  test("a future moment is its Unix milliseconds", () => {
    const at = new Date(NOW + 3_600_000)
    expect(parseMoment(localMoment(at), NOW)).toEqual({
      ok: true,
      at: new Date(localMoment(at)).getTime(),
    })
  })

  test("nothing, nonsense and the past are refused", () => {
    expect(parseMoment("", NOW).ok).toBe(false)
    expect(parseMoment("yesterday", NOW).ok).toBe(false)
    expect(parseMoment(localMoment(new Date(NOW - 60_000)), NOW).ok).toBe(false)
  })
})

describe("how an expiry reads", () => {
  test("none, short and long", () => {
    expect(ttlWord(-1)).toBe("No expiry")
    expect(ttlWord(45)).toBe("45s")
    expect(ttlWord(3700)).toBe("1h 1m")
    expect(ttlWord(86_400 * 3 + 3600)).toBe("3d 1h")
    // Centuries are years, not a number of days nobody can read.
    expect(ttlWord(86_400 * 365 * 7973)).toBe("7,973y")
  })

  test("what is left counts down from the reading and stops at zero", () => {
    expect(remainingMs(10_000, NOW, NOW + 4000)).toBe(6000)
    expect(remainingMs(10_000, NOW, NOW + 40_000)).toBe(0)
    expect(remainingMs(-1, NOW, NOW + 40_000)).toBe(-1)
  })
})

describe("a stream entry's id as a moment", () => {
  test("an id the server generated is a time", () => {
    expect(entryMoment("1790882554510-0")?.getTime()).toBe(1_790_882_554_510)
  })

  test("an id chosen by hand is not", () => {
    expect(entryMoment("1-0")).toBeNull()
    expect(entryMoment("0-1")).toBeNull()
    expect(entryMoment("abc")).toBeNull()
  })
})
