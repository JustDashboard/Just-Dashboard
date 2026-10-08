import { describe, expect, test } from "bun:test"
import {
  flowBytes,
  flowCounter,
  flowDateRange,
  flowOwnerName,
  flowPolicyProblem,
  flowReading,
  flowTotals,
  flowKernelTotals,
  flowIsKernelRow,
  observerReading,
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
  test("separates identical TCP channels and never promotes unknown evidence", () => {
    const native = { socket: { protocol: "tcp" }, txBytes: "257", rxBytes: "0" }
    const kernel = {
      ...native,
      evidence: "kernel_transport_payload_observed",
      observedTxBytes: "257",
      observedRxBytes: "0",
      observedPackets: "4",
      observedTxPackets: "3",
      observedRxPackets: "1",
      observedTxKnownPackets: "2",
      observedRxKnownPackets: "1",
      observedTxByteGaps: "1",
      observedRxByteGaps: "0",
    }
    const rows = [native, kernel, { ...native, evidence: "future_unknown_channel" }]
    expect(flowTotals(rows).tx.value).toBe(257n)
    expect(flowTotals(rows).tx.known).toBe(1)
    expect(flowIsKernelRow(kernel)).toBe(true)
    const observed = flowKernelTotals(rows)
    expect(observed.rows).toBe(1)
    expect(observed.tx.value).toBe(257n)
    expect(observed.rx.value).toBe(0n)
    expect(observed.txGaps.value).toBe(1n)
    expect(observed.txKnown.value).toBe(2n)
    expect(observed.txPackets.value).toBe(3n)
  })
  test("keeps missing kernel bytes and quality counts unknown while adding exact subtotals", () => {
    const kernel = (observedTxBytes, observedTxByteGaps) => ({
      socket: { protocol: "udp" },
      evidence: "kernel_transport_payload_observed",
      observedTxBytes,
      observedTxByteGaps,
    })
    expect(flowKernelTotals([]).tx.value).toBeNull()
    const observed = flowKernelTotals([
      kernel("9007199254740993", "0"),
      kernel("9007199254740993", "1"),
      kernel(null, undefined),
    ])
    expect(observed.tx.value).toBe(18014398509481986n)
    expect(observed.tx.unknown).toBe(1)
    expect(observed.txGaps.value).toBe(1n)
    expect(observed.txGaps.unknown).toBe(1)
    expect(observed.packets.value).toBeNull()
    expect(observerReading("interrupted").tone).toBe("warning")
    expect(observerReading("partial").label).toBe("Partial observer coverage")
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
