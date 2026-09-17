import { describe, expect, test } from "bun:test"
import { formatDelta, relativeChange, windowStat, windowStatSum } from "@/lib/metrics-summary"
import { csvColumns, rowsToCsv } from "@/lib/metrics-export"

const rows = [
  { ts: 1000, cpu: 10, cpuPeak: 40, rx: 100, tx: 50 },
  { ts: 2000, cpu: 30, cpuPeak: 95, rx: 300, tx: 100 },
  { ts: 3000, cpu: null, cpuPeak: null, rx: null, tx: null },
  { ts: 4000, cpu: 20, cpuPeak: 20, rx: 200, tx: 0 },
]

describe("windowStat", () => {
  test("averages the mean column and takes the peak from the peak column", () => {
    const stat = windowStat(rows, "cpu", "cpuPeak")
    expect(stat).toEqual({ mean: 20, peak: 95, peakTs: 2000, count: 3 })
  })

  test("skips gaps rather than counting them as zero", () => {
    expect(windowStat(rows, "cpu")?.count).toBe(3)
  })

  test("is null for a series with nothing in the window", () => {
    expect(windowStat(rows, "absent")).toBeNull()
  })

  test("sums several series per row before reducing", () => {
    expect(windowStatSum(rows, ["rx", "tx"])).toEqual({
      mean: 250,
      peak: 400,
      peakTs: 2000,
      count: 3,
    })
  })
})

describe("relativeChange", () => {
  test("is a share of the earlier figure", () => {
    expect(relativeChange(112, 100)).toBe(12)
    expect(formatDelta(relativeChange(92, 100))).toBe("−8%")
    expect(formatDelta(relativeChange(100.2, 100))).toBe("±0%")
  })

  test("refuses to divide by nothing", () => {
    expect(relativeChange(5, 0)).toBeNull()
    expect(formatDelta(null)).toBeNull()
  })
})

describe("rowsToCsv", () => {
  test("writes an ISO time, one column per series, and empty cells for gaps", () => {
    const csv = rowsToCsv(rows.slice(0, 3), csvColumns(rows))
    expect(csv.split("\n")[0]).toBe("time,cpu,cpuPeak,rx,tx")
    expect(csv.split("\n")[1]).toBe("1970-01-01T00:00:01.000Z,10,40,100,50")
    expect(csv.split("\n")[3]).toBe("1970-01-01T00:00:03.000Z,,,,")
  })
})
