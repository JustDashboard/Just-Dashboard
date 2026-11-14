import { expect, test } from "bun:test"
import { coveringWindow, spanBounds, spanFromParams, writeSpan } from "./span"

const query = (text) => new URLSearchParams(text)

test("an address names a recorded window or a range, and anything else is live", () => {
  expect(spanFromParams(query(""))).toBe("live")
  expect(spanFromParams(query("window=24h"))).toBe("24h")
  expect(spanFromParams(query("window=live"))).toBe("live")
  expect(spanFromParams(query("window=30d"))).toBe("live")
  expect(spanFromParams(query("from=1000&to=6400"))).toEqual({ from: 1000, to: 6400 })
  // Half a range, a reversed one and one that is not a number are not ranges.
  expect(spanFromParams(query("from=1000"))).toBe("live")
  expect(spanFromParams(query("from=6400&to=1000"))).toBe("live")
  expect(spanFromParams(query("from=soon&to=later&window=6h"))).toBe("6h")
})

test("a span is written back as the query that reads it", () => {
  const through = (span) => spanFromParams(writeSpan(query("tab=budgets"), span))
  expect(through("live")).toBe("live")
  expect(through("7d")).toBe("7d")
  expect(through({ from: 10, to: 20 })).toEqual({ from: 10, to: 20 })
  expect(writeSpan(query("tab=budgets&window=1h"), "live").toString()).toBe("tab=budgets")
  expect(writeSpan(query("window=1h"), { from: 10, to: 20 }).toString()).toBe("from=10&to=20")
  expect(writeSpan(query("from=10&to=20"), "6h").toString()).toBe("window=6h")
})

test("a range reads the named window that covers it", () => {
  const now = 1_000_000
  expect(coveringWindow("live", now)).toBe("1h")
  expect(coveringWindow("24h", now)).toBe("24h")
  expect(coveringWindow({ from: now - 5000, to: now - 100 }, now)).toBe("6h")
  expect(coveringWindow({ from: now - 40 * 86400, to: now - 39 * 86400 }, now)).toBe("7d")
  expect(spanBounds("live", now)).toEqual({ from: now - 900, to: now })
  expect(spanBounds("6h", now)).toEqual({ from: now - 21600, to: now })
})
