import { expect, test } from "bun:test"
import { formatLogTime, readLogView } from "./log-view"

// The settings outlive the code that wrote them: an operator who turned the
// time column off under the old switch must not find it back on.
test("the old timestamps switch becomes the time mode", () => {
  expect(readLogView({ wrap: true, timestamps: false, highlight: true }).time).toBe("off")
  expect(readLogView({ timestamps: true }).time).toBe("clock")
  expect(readLogView({ wrap: true }).time).toBe("clock")
})

test("a time mode wins over the old switch, and an unknown one falls back", () => {
  expect(readLogView({ time: "utc", timestamps: false }).time).toBe("utc")
  expect(readLogView({ time: "sundial" }).time).toBe("clock")
})

test("flags of the wrong type and unreadable values fall back to the defaults", () => {
  expect(readLogView({ wrap: "yes", dedupe: false })).toEqual({
    wrap: false,
    time: "clock",
    highlight: true,
    dedupe: false,
  })
  for (const raw of [null, "x", 3, []]) expect(readLogView(raw).time).toBe("clock")
})

test("each time mode reads the stamp its own way", () => {
  const at = "2026-09-23T05:21:42.600Z"
  expect(formatLogTime(at, "utc")).toBe("05:21:42Z")
  expect(formatLogTime(at, "delta", "2026-09-23T05:21:42.480Z")).toBe("+0.120s")
  expect(formatLogTime(at, "delta", "2026-09-23T05:20:30.600Z")).toBe("+1m 12s")
  // Nothing above it to measure from: the clock instead.
  expect(formatLogTime(at, "delta")).toBe(formatLogTime(at, "clock"))
  expect(formatLogTime(undefined, "clock")).toBe("—")
  expect(formatLogTime("garbage", "clock")).toBe("—")
})
