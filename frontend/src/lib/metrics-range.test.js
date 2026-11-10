import { expect, test } from "bun:test"
import {
  DEFAULT_RANGE,
  HISTORY_RANGES,
  RANGES,
  containerRows,
  coverageNote,
  historyRows,
  liveRows,
  liveStorageRows,
  memoryLimit,
  rangeSpec,
  retentionNote,
  storageRows,
  windowLabel,
  windowQuery,
  windowRefreshMs,
  windowSeconds,
} from "./metrics-range"

const T0 = Date.parse("2026-11-10T12:00:00Z")
const at = (seconds) => new Date(T0 + seconds * 1000).toISOString()

// ---------------------------------------------------------------- windows

test("each named range asks for its own buckets at its own cadence", () => {
  expect(RANGES.map((r) => [r.key, r.query, r.points, r.refreshMs, r.seconds])).toEqual([
    ["live", undefined, 0, 0, 300],
    ["1h", "1h", 240, 15_000, 3_600],
    ["6h", "6h", 288, 60_000, 21_600],
    ["24h", "24h", 288, 120_000, 86_400],
    ["7d", "7d", 336, 300_000, 604_800],
  ])
  // A bucket is never finer than the sampler's fifteen seconds on the hour,
  // and never coarser than half an hour on the week.
  for (const r of HISTORY_RANGES) {
    const bucket = r.seconds / r.points
    expect(bucket).toBeGreaterThanOrEqual(15)
    expect(bucket).toBeLessThanOrEqual(1800)
  }
})

test("the recorded ranges are the ones a server can be asked for, and the default is one of them", () => {
  expect(HISTORY_RANGES.map((r) => r.key)).toEqual(["1h", "6h", "24h", "7d"])
  expect(HISTORY_RANGES.map((r) => r.key)).toContain(DEFAULT_RANGE)
  expect(rangeSpec("nonsense").key).toBe("1h")
})

test("a named window is asked for by its name, a dragged one by its two ends in seconds", () => {
  expect(windowQuery({ key: "6h" }, 288)).toEqual({ range: "6h", points: 288 })
  // The live range has nothing to ask the server; if it is asked, it asks for the hour.
  expect(windowQuery({ key: "live" }, 240)).toEqual({ range: "1h", points: 240 })
  expect(windowQuery({ key: "6h", from: T0 + 999, to: T0 + 300_500 }, 120)).toEqual({
    from: T0 / 1000,
    to: T0 / 1000 + 300,
    points: 120,
  })
  // One end alone is not a span.
  expect(windowQuery({ key: "24h", from: T0 }, 288)).toEqual({ range: "24h", points: 288 })
})

test("a dragged span is as wide as it was dragged, and is read once", () => {
  expect(windowSeconds({ key: "7d" })).toBe(604_800)
  expect(windowSeconds({ key: "7d", from: T0, to: T0 + 90_000 })).toBe(90)
  expect(windowRefreshMs({ key: "1h" })).toBe(15_000)
  expect(windowRefreshMs({ key: "live" })).toBe(0)
  // A fixed span in the past does not change, whatever range it was dragged on.
  expect(windowRefreshMs({ key: "1h", from: T0, to: T0 + 90_000 })).toBe(0)
})

test("a dragged span is named by the unit it is best read in", () => {
  const span = (seconds) => windowLabel({ key: "1h", from: T0, to: T0 + seconds * 1000 })
  expect(windowLabel({ key: "24h" })).toBe("24h")
  expect(span(45)).toBe("45s")
  expect(span(89)).toBe("89s")
  expect(span(90)).toBe("2m")
  expect(span(15 * 60)).toBe("15m")
  expect(span(89 * 60)).toBe("89m")
  expect(span(90 * 60)).toBe("2h")
  expect(span(36 * 3600)).toBe("36h")
  expect(span(48 * 3600)).toBe("2d")
  expect(span(5 * 86_400)).toBe("5d")
})

// ------------------------------------------------------------------- rows

const hostPoint = (seconds, cpu) => ({
  ts: at(seconds),
  cpu,
  cpuPeak: cpu + 10,
  mem: 40,
  memPeak: 42,
  swap: 1,
  rx: 100,
  rxPeak: 200,
  tx: 50,
  txPeak: 60,
  diskPercent: 71,
  diskRead: 5,
  diskReadPeak: 6,
  diskWrite: 7,
  diskWritePeak: 8,
  load1: 0.5,
  load5: 0.4,
  load15: 0.3,
})

test("a recorded series is one row a bucket, mean and peak side by side", () => {
  const rows = historyRows({
    stepSeconds: 15,
    points: [hostPoint(0, 10), hostPoint(15, 20), hostPoint(30, 30)],
  })
  expect(rows.map((r) => r.ts)).toEqual([T0, T0 + 15_000, T0 + 30_000])
  expect(rows.map((r) => [r.cpu, r.cpuPeak])).toEqual([
    [10, 20],
    [20, 30],
    [30, 40],
  ])
  expect(rows[0].disk).toBe(71)
  expect(rows[0].load1).toBe(0.5)
})

test("a hole in the record is a row of nulls one bucket after the last reading", () => {
  const rows = historyRows({
    stepSeconds: 15,
    // Thirty seconds late is a hole; twenty is a sampler that drifted.
    points: [hostPoint(0, 10), hostPoint(20, 11), hostPoint(50, 12), hostPoint(65, 13)],
  })
  expect(rows.map((r) => [r.ts - T0, r.cpu])).toEqual([
    [0, 10],
    [20_000, 11],
    [35_000, null],
    [50_000, 12],
    [65_000, 13],
  ])
  const gap = rows[2]
  // Every series stops at the hole: a key left out would be joined across it.
  const drawn = Object.keys(rows[0]).filter((key) => !["t", "ts", "at"].includes(key))
  expect(drawn.length).toBeGreaterThan(30)
  for (const key of drawn) expect([key, gap[key]]).toEqual([key, null])
  expect(gap.t).toBe("")
})

test("a reading whose time cannot be read is left out rather than drawn at nowhere", () => {
  const rows = historyRows({
    stepSeconds: 15,
    points: [hostPoint(0, 10), { ...hostPoint(15, 99), ts: "not a time" }, hostPoint(15, 20)],
  })
  expect(rows.map((r) => r.cpu)).toEqual([10, 20])
})

test("a step of nothing is read as a second, so one reading does not open a hole after every other", () => {
  const rows = historyRows({ stepSeconds: 0, points: [hostPoint(0, 1), hostPoint(1, 2)] })
  expect(rows.map((r) => r.cpu)).toEqual([1, 2])
})

const livePoint = (seconds, patch = {}) => ({
  t: `t${seconds}`,
  ts: T0 + seconds * 1000,
  cpu: 12,
  mem: 34,
  swap: 0,
  rx: 1,
  tx: 2,
  disk: 61,
  dread: 3,
  dwrite: 4,
  cpuUser: 8,
  cpuSystem: 3,
  cpuIowait: 1,
  cpuSteal: 0,
  psiCpu: 0.4,
  psiMem: 0,
  psiIo: 1.5,
  dreads: 10,
  dwrites: 20,
  await: 0.7,
  busy: 2,
  tcp: 300,
  procsBlocked: 0,
  ...patch,
})

test("the live buffer is drawn at its own resolution: no peaks, and nothing it does not carry", () => {
  const [row] = liveRows([livePoint(0)])
  expect(row.ts).toBe(T0)
  expect([row.cpu, row.mem, row.rx, row.tx, row.diskRead, row.diskWrite]).toEqual([
    12, 34, 1, 2, 3, 4,
  ])
  for (const key of Object.keys(row).filter((k) => k.endsWith("Peak"))) expect(row[key]).toBeNull()
  // Read from the newest snapshot or from the record, never from the buffer.
  for (const key of ["load1", "load5", "load15", "rtt", "retransPct", "tcpTimeWait"]) {
    expect([key, row[key]]).toEqual([key, null])
  }
})

test("a kernel without pressure figures draws no pressure line, not one below the axis", () => {
  const [row] = liveRows([livePoint(0, { psiCpu: -1, psiMem: -1, psiIo: -1 })])
  expect([row.psiCpu, row.psiMem, row.psiIo]).toEqual([null, null, null])
  const [measured] = liveRows([livePoint(0)])
  expect([measured.psiCpu, measured.psiMem, measured.psiIo]).toEqual([0.4, 0, 1.5])
})

// ------------------------------------------------------------- containers

const containerPoint = (seconds, patch = {}) => ({
  ts: at(seconds),
  cpu: 5,
  cpuPeak: 9,
  memBytes: 1000,
  memBytesPeak: 1200,
  memLimit: 4096,
  netRx: 10,
  netTx: 20,
  blockRead: 30,
  blockWrite: 40,
  pids: 7,
  ...patch,
})

test("a container's rows take a rate as its own peak where the record kept none", () => {
  const [bare, peaked] = containerRows({
    stepSeconds: 15,
    points: [
      containerPoint(0),
      containerPoint(15, { netRxPeak: 99, blockWritePeak: 77, pidsPeak: 12 }),
    ],
  })
  expect([bare.netRxPeak, bare.netTxPeak, bare.blockReadPeak, bare.blockWritePeak]).toEqual([
    10, 20, 30, 40,
  ])
  expect(bare.pidsPeak).toBe(7)
  expect([peaked.netRxPeak, peaked.blockWritePeak, peaked.pidsPeak]).toEqual([99, 77, 12])
  expect([bare.mem, bare.memPeak]).toEqual([1000, 1200])
})

test("a container stopped and started again leaves a hole, not a flat line", () => {
  const rows = containerRows({
    stepSeconds: 15,
    points: [containerPoint(0), containerPoint(120)],
  })
  expect(rows.map((r) => [r.ts - T0, r.cpu])).toEqual([
    [0, 5],
    [15_000, null],
    [120_000, 5],
  ])
})

test("the limit line is the highest ceiling the window saw, and nothing where there was none", () => {
  expect(memoryLimit(undefined)).toBe(0)
  expect(memoryLimit({ stepSeconds: 15, points: [] })).toBe(0)
  expect(memoryLimit({ stepSeconds: 15, points: [containerPoint(0, { memLimit: 0 })] })).toBe(0)
  expect(
    memoryLimit({
      stepSeconds: 15,
      points: [containerPoint(0, { memLimit: 2048 }), containerPoint(15, { memLimit: 4096 })],
    }),
  ).toBe(4096)
})

// ---------------------------------------------------------------- storage

const mountPoint = (seconds, usedPercent, inodesPercent = 3) => ({
  ts: at(seconds),
  usedPercent,
  inodesPercent,
})

test("each filesystem is a series keyed by its place, never by a path a chart would read as nested", () => {
  const { rows, series } = storageRows({
    stepSeconds: 60,
    mounts: [
      { mountpoint: "/", points: [mountPoint(0, 40), mountPoint(60, 41)] },
      { mountpoint: "/mnt/data.disk", points: [mountPoint(0, 70, 9), mountPoint(60, 71, 9)] },
    ],
  })
  expect(series.map((s) => [s.key, s.mountpoint])).toEqual([
    ["m0", "/"],
    ["m1", "/mnt/data.disk"],
  ])
  expect(rows.map((r) => [r.m0, r.m1, r.m0i, r.m1i])).toEqual([
    [40, 70, 3, 9],
    [41, 71, 3, 9],
  ])
})

test("a disk attached halfway through the window joins the rows at the time it appeared", () => {
  const { rows } = storageRows({
    stepSeconds: 60,
    mounts: [
      { mountpoint: "/", points: [mountPoint(0, 40), mountPoint(60, 41), mountPoint(120, 42)] },
      { mountpoint: "/data", points: [mountPoint(120, 5)] },
    ],
  })
  expect(rows.map((r) => [r.ts - T0, r.m0, r.m1])).toEqual([
    [0, 40, undefined],
    [60_000, 41, undefined],
    [120_000, 42, 5],
  ])
})

test("the storage rows are in the order of time and break where the record does", () => {
  const { rows } = storageRows({
    stepSeconds: 60,
    mounts: [{ mountpoint: "/", points: [mountPoint(300, 43), mountPoint(0, 40)] }],
  })
  expect(rows.map((r) => [r.ts - T0, r.m0])).toEqual([
    [0, 40],
    [60_000, undefined],
    [300_000, 43],
  ])
})

test("the colours of the filesystems come round again after the fifth", () => {
  const { series } = storageRows({
    stepSeconds: 60,
    mounts: Array.from({ length: 6 }, (_, i) => ({ mountpoint: `/m${i}`, points: [] })),
  })
  expect(series[5].color).toBe(series[0].color)
  expect(new Set(series.slice(0, 5).map((s) => s.color)).size).toBe(5)
})

test("the live feed has one storage line: the mount where there is one, the fullest where there are several", () => {
  const buffer = [livePoint(0), livePoint(2, { disk: 62 })]
  const one = liveStorageRows(buffer, [{ mountpoint: "/" }])
  expect(one.series.map((s) => s.mountpoint)).toEqual(["/"])
  expect(one.rows.map((r) => r.m0)).toEqual([61, 62])
  const several = liveStorageRows(buffer, [{ mountpoint: "/" }, { mountpoint: "/data" }])
  expect(several.series.map((s) => s.mountpoint)).toEqual(["fullest"])
  expect(liveStorageRows(buffer, []).series.map((s) => s.mountpoint)).toEqual(["fullest"])
})

// ------------------------------------------------------------------ notes

test("a record younger than its window says when it starts, with a minute's grace", () => {
  const window = (earliest) => ({
    from: at(0),
    earliest,
    stepSeconds: 15,
    retentionSeconds: 604_800,
  })
  expect(coverageNote(undefined)).toBeNull()
  expect(coverageNote(window(null))).toBeNull()
  expect(coverageNote(window("not a time"))).toBeNull()
  expect(coverageNote(window(at(-3600)))).toBeNull()
  expect(coverageNote(window(at(60)))).toBeNull()
  expect(coverageNote(window(at(61)))).toStartWith("recorded from ")
})

test("a range wider than what is kept says how long history is kept", () => {
  const kept = (retentionSeconds) => ({
    from: at(0),
    earliest: null,
    stepSeconds: 15,
    retentionSeconds,
  })
  expect(retentionNote(undefined, rangeSpec("7d"))).toBeNull()
  expect(retentionNote(kept(604_800), rangeSpec("7d"))).toBeNull()
  expect(retentionNote(kept(86_400), rangeSpec("24h"))).toBeNull()
  expect(retentionNote(kept(86_400), rangeSpec("7d"))).toBe("history is kept for 24h")
})
