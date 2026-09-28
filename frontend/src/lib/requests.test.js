import { describe, expect, test } from "bun:test"
import {
  CLASS_DOT,
  CLASS_TEXT,
  latencyBracket,
  latencyTone,
  methodEmphasis,
  statusWord,
} from "./requests"

// The request console, its facets and the host's log console all colour a
// code from one map. These pin what the reader relies on: a column of 2xx
// scans green, and a chip's dot is the colour of the codes it filters.
describe("a status family's colour", () => {
  test("every family that answered takes its own hue", () => {
    expect(CLASS_TEXT["2xx"]).toBe("text-success")
    expect(CLASS_TEXT["3xx"]).toBe("text-[var(--tag-cyan)]")
    expect(CLASS_TEXT["4xx"]).toBe("text-warning")
    expect(CLASS_TEXT["5xx"]).toBe("text-destructive")
  })

  test("the chip's dot is the code's colour", () => {
    for (const family of ["2xx", "3xx", "4xx", "5xx"]) {
      expect(CLASS_DOT[family]).toBe(CLASS_TEXT[family].replace(/^text-/, "bg-"))
    }
  })
})

describe("a method as a word", () => {
  test("reads stay quiet and writes step forward, DELETE included", () => {
    expect(methodEmphasis("get")).toBe("text-muted-foreground")
    expect(methodEmphasis("POST")).toBe(methodEmphasis("DELETE"))
    expect(methodEmphasis("DELETE")).not.toContain("destructive")
  })
})

describe("a status code in words", () => {
  test("names the codes a deployment answers with", () => {
    expect(statusWord(200)).toBe("OK")
    expect(statusWord(405)).toBe("Method not allowed")
    expect(statusWord(499)).toBe("Client closed")
    expect(statusWord(502)).toBe("Bad gateway")
  })

  test("falls back to the family, which is never wrong", () => {
    expect(statusWord(418)).toBe("Client error")
    expect(statusWord(599)).toBe("Server error")
    expect(statusWord(207)).toBe("OK")
    expect(statusWord(0)).toBe("Unreadable")
  })
})

describe("how slow a request was", () => {
  const latency = { p50: 80, p75: 120, p90: 300, p95: 412, p99: 2840, max: 3120, mean: 78 }

  test("past a second is slow", () => {
    expect(latencyTone(1000)).toBe("default")
    expect(latencyTone(1001)).toBe("warning")
    expect(latencyTone(undefined)).toBe("default")
  })

  test("says where a request sits among the window's", () => {
    expect(latencyBracket(3000, latency)).toBe("in the slowest hundredth")
    expect(latencyBracket(500, latency)).toBe("in the slowest tenth")
    // Past the p90 and short of the p95 is still the slowest tenth.
    expect(latencyBracket(350, latency)).toBe("in the slowest tenth")
    expect(latencyBracket(200, latency)).toBe("slower than most")
    expect(latencyBracket(80, latency)).toBe("about the median")
    expect(latencyBracket(10, latency)).toBe("faster than the median")
    expect(latencyBracket(10, undefined)).toBeUndefined()
  })
})
