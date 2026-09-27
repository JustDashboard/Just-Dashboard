import { describe, expect, test } from "bun:test"
import { containerRates, containerRateLabel } from "./container-usage"

function sample(second, bytes) {
  return {
    id: "web",
    name: "web",
    ts: new Date(1_800_000_000_000 + second * 1000).toISOString(),
    cpuPercent: 150,
    cpuReady: true,
    memUsage: 100,
    memLimit: 1000,
    memLimited: true,
    memPercent: 10,
    netRx: bytes,
    netTx: bytes * 2,
    blockRead: bytes * 3,
    blockWrite: bytes * 4,
    pids: 12,
    onlineCpus: 4,
    cpuTotal: 1000 + second * 100,
    systemCpu: 100000 + second * 400,
    cpuPeriods: second * 10,
    cpuThrottledPeriods: second * 2,
    networkAvailable: true,
    blockAvailable: true,
    networks: {
      eth0: {
        rxBytes: bytes,
        txBytes: bytes * 2,
        rxPackets: bytes / 100,
        txPackets: bytes / 50,
        rxErrors: 0,
        txErrors: 2,
        rxDropped: 1,
        txDropped: 0,
      },
    },
  }
}

describe("container traffic rates", () => {
  test("labels fractional-byte rates, true zero and absent readings distinctly", () => {
    expect(containerRateLabel(0.5)).toBe("0.50 B/s")
    expect(containerRateLabel(0)).toBe("0 B/s")
    expect(containerRateLabel(null)).toBe("—")
    expect(containerRateLabel(2048)).toBe("2.0 KB/s")
  })
  test("uses sample elapsed time and preserves both directions and packet counters", () => {
    const rates = containerRates(sample(3, 1600), sample(0, 1000))
    expect(rates.rx).toBe(200)
    expect(rates.tx).toBe(400)
    expect(rates.read).toBe(600)
    expect(rates.write).toBe(800)
    expect(rates.throttledPercent).toBe(20)
    expect(rates.interfaces[0].rxPacketsRate).toBe(2)
    expect(rates.interfaces[0].txErrors).toBe(2)
  })

  test("first reading, resets, replacement, gaps and reversed timestamps are unknown", () => {
    const before = sample(0, 1000)
    for (const [after, previous] of [
      [sample(3, 1600), undefined],
      [{ ...sample(3, 1600), id: "other" }, before],
      [{ ...sample(3, 1600), cpuTotal: 1 }, before],
      [sample(20, 1600), before],
      [sample(0, 1600), before],
      [before, sample(3, 1600)],
    ]) {
      const rates = containerRates(after, previous)
      expect(rates.rx).toBeNull()
      expect(rates.read).toBeNull()
      expect(rates.throttledPercent).toBeNull()
    }
  })

  test("a counter reset is not a negative rate, zero is a valid idle reading", () => {
    expect(containerRates(sample(3, 0), sample(0, 1000)).rx).toBeNull()
    expect(containerRates(sample(3, 0), sample(0, 0)).rx).toBe(0)
    expect(containerRates(sample(3, 0), sample(0, 0)).read).toBe(0)
  })

  test("new or removed interfaces require a new aggregate baseline", () => {
    const before = sample(0, 1000)
    const after = sample(3, 1600)
    after.networks.eth1 = { ...after.networks.eth0, rxBytes: 10000000 }
    const added = containerRates(after, before)
    expect(added.rx).toBeNull()
    expect(added.interfaces.find((entry) => entry.name === "eth0")?.rx).toBe(200)
    expect(added.interfaces.find((entry) => entry.name === "eth1")?.rx).toBeNull()
    const removed = containerRates(sample(6, 2000), after)
    expect(removed.rx).toBeNull()
  })

  test("absent telemetry is never zero traffic", () => {
    const absent = { ...sample(3, 0), networks: {}, networkAvailable: false, blockAvailable: false }
    const rates = containerRates(absent, sample(0, 0))
    expect(rates.rx).toBeNull()
    expect(rates.tx).toBeNull()
    expect(rates.read).toBeNull()
    expect(rates.interfaces).toEqual([])
  })
})
