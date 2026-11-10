import { expect, test } from "bun:test"
import { LATENCY_SHARE, latencyHead, latencyTerm, latencyWords } from "./latency-words"

test("a mark is named by the share of the whole it stands for", () => {
  expect(LATENCY_SHARE).toEqual({
    p50: "Half",
    p90: "9 in 10",
    p95: "19 in 20",
    p99: "99 in 100",
  })
})

test("a head, a clause and the term behind a tooltip are one vocabulary", () => {
  expect(latencyHead("p95")).toBe("19 in 20 within")
  expect(latencyHead("p50")).toBe("Half within")
  expect(latencyWords("p95", "180ms")).toBe("19 in 20 inside 180ms")
  expect(latencyWords("p50", "24ms")).toBe("half inside 24ms")
  // The term stays, for the reader who looks for it, only where it is asked for.
  expect(latencyTerm("p95")).toBe("19 in 20 finished within this (the 95th percentile, p95)")
  expect(latencyTerm("p99", "sessions ended")).toBe(
    "99 in 100 sessions ended within this (the 99th percentile, p99)",
  )
})
