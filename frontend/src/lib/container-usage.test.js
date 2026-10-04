import { describe, expect, test } from "bun:test"
import {
  appendLive,
  byteScale,
  containerRates,
  containerRateLabel,
  cpuScale,
  liveRow,
  LIVE_WINDOW_MS,
  peakOf,
} from "./container-usage"

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

describe("the live window", () => {
  test("a row carries each direction's rate on its own, never their sum", () => {
    const row = liveRow(sample(1, 1600), sample(0, 1000))
    expect(row.netRx).toBe(600)
    expect(row.netTx).toBe(1200)
    expect(row.cpu).toBe(150)
    expect(row.mem).toBe(100)
    expect(row.pids).toBe(12)
  })

  test("the first frame and a frame Docker had no CPU interval for are breaks, not zeros", () => {
    const first = liveRow(sample(0, 1000))
    expect(first.netRx).toBeNull()
    expect(first.netTx).toBeNull()
    expect(liveRow({ ...sample(1, 1000), cpuReady: false }, sample(0, 1000)).cpu).toBeNull()
  })

  test("frames append in order and a repeated or older frame changes nothing", () => {
    let rows = appendLive([], sample(0, 1000))
    rows = appendLive(rows, sample(1, 1500), sample(0, 1000))
    expect(rows.map((row) => row.netRx)).toEqual([null, 500])
    expect(appendLive(rows, sample(1, 1500), sample(1, 1500))).toBe(rows)
    expect(appendLive(rows, sample(0, 1000), sample(1, 1500))).toBe(rows)
  })

  test("a stall opens a gap instead of a line across the time nobody measured", () => {
    let rows = appendLive([], sample(0, 1000))
    rows = appendLive(rows, sample(1, 1500), sample(0, 1000))
    rows = appendLive(rows, sample(40, 9000), sample(1, 1500))
    expect(rows).toHaveLength(4)
    expect(rows[2].cpu).toBeNull()
    expect(rows[2].netRx).toBeNull()
    expect(rows[2].ts).toBeGreaterThan(rows[1].ts)
    expect(rows[3].netRx).toBeNull()
  })

  test("rows older than the window fall off the front", () => {
    const seconds = LIVE_WINDOW_MS / 1000
    let rows = []
    let previous
    for (let second = 0; second <= seconds + 30; second += 1) {
      const frame = sample(second, 1000 + second * 10)
      rows = appendLive(rows, frame, previous)
      previous = frame
    }
    expect(rows).toHaveLength(seconds + 1)
    expect(rows.at(-1).ts - rows[0].ts).toBe(LIVE_WINDOW_MS)
    expect(rows.every((row) => row.netRx === 10)).toBe(true)
  })
})

describe("chart scales", () => {
  test("a byte axis ends on a round figure above the peak and ticks at its quarters", () => {
    const MB = 1024 * 1024
    expect(byteScale(3.1 * MB)).toEqual({
      domain: [0, 4 * MB],
      ticks: [0, MB, 2 * MB, 3 * MB, 4 * MB],
    })
    expect(byteScale(8 * MB).domain[1]).toBe(12 * MB)
    expect(byteScale(200 * 1024).domain[1]).toBe(256 * 1024)
    expect(byteScale(0).domain[1]).toBe(1024)
  })

  test("the processor axis keeps an idle container's hundredths readable and grows by cores", () => {
    expect(cpuScale(0.2)).toEqual({ domain: [0, 1], ticks: [0, 0.25, 0.5, 0.75, 1] })
    expect(cpuScale(37).domain[1]).toBe(40)
    expect(cpuScale(95).domain[1]).toBe(200)
    expect(cpuScale(350).domain[1]).toBe(400)
  })

  test("a peak reads every named series and ignores breaks", () => {
    const rows = [
      { netRx: 10, netTx: null, netRxPeak: 40 },
      { netRx: null, netTx: 25, netRxPeak: null },
    ]
    expect(peakOf(rows, ["netRx", "netTx"])).toBe(25)
    expect(peakOf(rows, ["netRx", "netTx", "netRxPeak"])).toBe(40)
    expect(peakOf([], ["netRx"])).toBe(0)
  })
})
