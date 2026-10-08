import { describe, expect, test } from "bun:test"
import {
  flowBytes,
  flowCounter,
  flowDateRange,
  flowOwnerName,
  flowPolicyProblem,
  flowReading,
  flowTotals,
  yesterdayUTC,
} from "./network-flows"

describe("native socket evidence", () => {
  test("keeps exact decimal uint64 and distinguishes unknown from measured zero", () => {
    expect(flowCounter("9007199254740993")).toBe(9007199254740993n)
    expect(flowCounter(null)).toBeNull()
    expect(flowCounter("18446744073709551616")).toBeNull()
    expect(flowCounter("1.2")).toBeNull()
    expect(flowBytes("0")).toBe("0 B")
    expect(flowBytes(null)).toBe("Unknown")
    expect(flowBytes("131072")).toBe("128.0 KiB")
  })
  test("adds only measured TCP deltas without inventing UDP bytes or empty-period totals", () => {
    const row = (protocol, txBytes) => ({
      socket: { protocol },
      txBytes,
      rxBytes: null,
      retransmissions: null,
    })
    expect(flowTotals([]).tx.value).toBeNull()
    expect(flowTotals([row("udp", "10000")]).tx.value).toBeNull()
    const summary = flowTotals([
      row("tcp", "9007199254740993"),
      row("tcp", "5"),
      row("tcp", null),
      row("udp", "10000"),
    ])
    expect(summary.tx.value).toBe(9007199254740998n)
    expect(summary.tx.known).toBe(2)
    expect(summary.rx.value).toBeNull()
  })
  test("UTC yesterday and valid day boundaries do not depend on browser timezone", () => {
    expect(yesterdayUTC(new Date("2026-10-08T00:10:00Z"))).toBe("2026-10-07")
    expect(flowDateRange("2026-10-07")).toEqual({
      from: "2026-10-07T00:00:00.000Z",
      to: "2026-10-08T00:00:00.000Z",
    })
    expect(flowDateRange("2026-02-31")).toBeNull()
    expect(flowDateRange("not-a-day")).toBeNull()
  })
  test("off, stale and unavailable do not claim complete or current accounting", () => {
    expect(flowReading("off").label).toBe("Recorder off")
    expect(flowReading("stale").tone).toBe("warning")
    expect(flowReading("unavailable").tone).toBe("unknown")
    expect(flowOwnerName({ status: "unknown", containerName: "unverified" })).toBe("Owner unknown")
    expect(flowPolicyProblem("9", "7")).toContain("10–300")
    expect(flowPolicyProblem("30", "32")).toContain("1–31")
    expect(flowPolicyProblem("30", "7")).toBeNull()
  })
})
