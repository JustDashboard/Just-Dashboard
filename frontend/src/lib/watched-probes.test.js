import { describe, expect, test } from "bun:test"
import { connectTimes, isProbe, probeStatus } from "./watched-probes"

describe("watched network probes", () => {
  test("a probe reads whether it connected, and how fast", () => {
    expect(probeStatus({ ok: true, state: "connected", ms: 3 })).toEqual({
      label: "connects in 3 ms",
      tone: "running",
    })
    expect(probeStatus({ ok: false, state: "refused", error: "connection refused" })).toEqual({
      label: "refused",
      tone: "danger",
    })
    expect(probeStatus(undefined).tone).toBe("unknown")
  })

  test("only probes are probes, and only connections count as times", () => {
    expect(isProbe({ kind: "tcp" })).toBe(true)
    expect(isProbe({ kind: "tls" })).toBe(false)
    expect(isProbe({})).toBe(false)
    expect(
      connectTimes([
        { checkedAt: "a", ms: 4 },
        { checkedAt: "b", error: "refused" },
        { checkedAt: "c", ms: 6 },
      ]),
    ).toEqual([4, 6])
    expect(connectTimes([{ checkedAt: "a" }])).toEqual([0])
    expect(probeStatus({ ok: true, state: "connected" }).label).toBe("connects in under 1 ms")
  })
})
