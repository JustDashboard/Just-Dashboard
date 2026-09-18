import { clock, timestamp } from "@/lib/format"
import type {
  ContainerHistory,
  ContainerHistoryPoint,
  MetricsHistory,
  MetricsHistoryPoint,
  StorageHistory,
} from "@/lib/types"
import type { MountStats } from "@/lib/types"
import type { MetricsPoint } from "@/lib/metrics-store"

/**
 * The windows the Overview charts can be drawn over.
 *
 * "live" is the two-second socket feed the browser accumulates while the
 * dashboard is open; everything else is read back from the server, which has
 * been sampling on its own timer whether or not anybody was looking. The two
 * are deliberately never spliced into one line: the cadences differ by two
 * orders of magnitude, and a chart that draws twenty coarse points and a
 * hundred fine ones at equal spacing is lying about when things happened.
 */
export type RangeKey = "live" | "1h" | "6h" | "24h" | "7d"

export type RangeSpec = {
  key: RangeKey
  label: string
  /** What the server is asked for. Absent for the live buffer. */
  query?: string
  /** How many buckets to request — roughly one per two horizontal pixels. */
  points: number
  /** How often to re-read. A week of history does not change every 15 seconds. */
  refreshMs: number
  /** Width of the window, used for the "recording since" note. */
  seconds: number
}

export const RANGES: RangeSpec[] = [
  { key: "live", label: "Live", points: 0, refreshMs: 0, seconds: 300 },
  { key: "1h", label: "1h", query: "1h", points: 240, refreshMs: 15_000, seconds: 3_600 },
  { key: "6h", label: "6h", query: "6h", points: 288, refreshMs: 60_000, seconds: 21_600 },
  { key: "24h", label: "24h", query: "24h", points: 288, refreshMs: 120_000, seconds: 86_400 },
  { key: "7d", label: "7d", query: "7d", points: 336, refreshMs: 300_000, seconds: 604_800 },
]

/**
 * The windows that come from the server.
 *
 * A container has no in-browser live buffer to fall back on — nothing
 * accumulates its stats across a page load — so its charts offer only the
 * recorded ranges rather than a "Live" option that would draw nothing.
 */
export const HISTORY_RANGES = RANGES.filter((r) => r.query)

export function rangeSpec(key: RangeKey): RangeSpec {
  return RANGES.find((r) => r.key === key) ?? RANGES[1]
}

export const DEFAULT_RANGE: RangeKey = "1h"

const STORAGE_KEY = "just-dashboard.metrics.range"

/**
 * The chosen window, held outside React so it can be read during render
 * without an effect.
 *
 * It lives in localStorage rather than sessionStorage: an operator who always
 * wants the last day should not have to say so again in every new tab. Reading
 * it through useSyncExternalStore is what keeps that honest across the server
 * render — the server has no localStorage, so it renders the default and React
 * swaps in the stored value after hydration, rather than the two disagreeing.
 */
let current: RangeKey | null = null
const rangeListeners = new Set<() => void>()

export function subscribeRange(listener: () => void) {
  rangeListeners.add(listener)
  return () => {
    rangeListeners.delete(listener)
  }
}

export function currentRange(): RangeKey {
  if (current === null) current = readStoredRange()
  return current
}

/** What the server renders. It cannot know the preference, so it draws the default. */
export function serverRange(): RangeKey {
  return DEFAULT_RANGE
}

/**
 * The window a set of charts is drawn over: either one of the named ranges, or
 * an explicit span the reader selected by dragging across a chart.
 *
 * A fixed list of five ranges is the thing every lightweight monitor gets
 * wrong. "What happened between 03:10 and 03:25" is the actual question after
 * a chart has shown you roughly when something went wrong, and answering it by
 * picking "6h" and squinting is what sends people to Grafana. Zooming keeps
 * the answer in the same page.
 */
export type MetricsWindow = {
  key: RangeKey
  /** Epoch ms. Present only on a zoomed window. */
  from?: number
  to?: number
}

/** Query parameters for a window, whichever kind it is. */
export function windowQuery(win: MetricsWindow, points: number): Record<string, string | number> {
  if (win.from !== undefined && win.to !== undefined) {
    // Unix seconds, which is what the backend's parseInstant reads without a
    // formatting round trip.
    return { from: Math.floor(win.from / 1000), to: Math.floor(win.to / 1000), points }
  }
  return { range: rangeSpec(win.key).query ?? "1h", points }
}

/** How wide the window is, in seconds — for the retention note. */
export function windowSeconds(win: MetricsWindow): number {
  if (win.from !== undefined && win.to !== undefined) return (win.to - win.from) / 1000
  return rangeSpec(win.key).seconds
}

/**
 * How often to re-read a window.
 *
 * A zoomed window is a fixed span in the past: it does not change, so it is
 * fetched once and left alone rather than re-requested on the range's cadence.
 */
export function windowRefreshMs(win: MetricsWindow): number {
  if (win.from !== undefined) return 0
  return rangeSpec(win.key).refreshMs
}

/** A label for a zoomed span, used where a range would show "6h". */
export function windowLabel(win: MetricsWindow): string {
  if (win.from === undefined || win.to === undefined) return rangeSpec(win.key).label
  const seconds = Math.round((win.to - win.from) / 1000)
  if (seconds < 90) return `${seconds}s`
  if (seconds < 5400) return `${Math.round(seconds / 60)}m`
  if (seconds < 172800) return `${Math.round(seconds / 3600)}h`
  return `${Math.round(seconds / 86400)}d`
}

export function storeRange(key: RangeKey) {
  if (current === key) return
  current = key
  try {
    window.localStorage.setItem(STORAGE_KEY, key)
  } catch {
    // Private-mode storage refusals cost a remembered preference, not a chart.
  }
  for (const listener of rangeListeners) listener()
}

function readStoredRange(): RangeKey {
  if (typeof window === "undefined") return DEFAULT_RANGE
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (raw && RANGES.some((r) => r.key === raw)) return raw as RangeKey
  } catch {
    // As above.
  }
  return DEFAULT_RANGE
}

/**
 * One row of chart data. Peaks are present only on server-backed ranges; the
 * live feed is already at its own resolution and has no bucket to peak within.
 *
 * A null in any series is a break in the record — the dashboard was not
 * running — and recharts draws it as a gap rather than joining the two sides.
 * That distinction matters: a straight line across four hours of downtime
 * reads as four hours of steady load.
 */
export type ChartRow = {
  t: string
  ts: number
  /** Full timestamp for the tooltip; the axis label alone is ambiguous past a day. */
  at: string
  cpu: number | null
  cpuPeak: number | null
  mem: number | null
  memPeak: number | null
  swap: number | null
  rx: number | null
  rxPeak: number | null
  tx: number | null
  txPeak: number | null
  /** Fullest filesystem, as a percentage. */
  disk: number | null
  diskRead: number | null
  diskReadPeak: number | null
  diskWrite: number | null
  diskWritePeak: number | null

  /** The CPU breakdown, which stacks to the same total the `cpu` line draws. */
  cpuUser: number | null
  cpuSystem: number | null
  cpuIowait: number | null
  cpuSteal: number | null

  /** Pressure: null where the kernel does not report it, so the chart shows a
   *  gap rather than a confident zero. */
  psiCpu: number | null
  psiCpuPeak: number | null
  psiMem: number | null
  psiMemPeak: number | null
  psiIo: number | null
  psiIoPeak: number | null

  diskReads: number | null
  diskReadsPeak: number | null
  diskWrites: number | null
  diskWritesPeak: number | null
  diskAwait: number | null
  diskAwaitPeak: number | null
  diskBusy: number | null
  diskBusyPeak: number | null

  tcp: number | null
  tcpPeak: number | null
  tcpTimeWait: number | null

  load1: number | null
  load5: number | null
  load15: number | null
  procsBlocked: number | null
}

/** Turns the live socket buffer into chart rows. No peaks, no gaps to bridge. */
export function liveRows(history: MetricsPoint[]): ChartRow[] {
  return history.map((p) => ({
    t: p.t,
    ts: p.ts,
    at: new Date(p.ts).toLocaleString(),
    cpu: p.cpu,
    cpuPeak: null,
    mem: p.mem,
    memPeak: null,
    swap: p.swap,
    rx: p.rx,
    rxPeak: null,
    tx: p.tx,
    txPeak: null,
    disk: p.disk,
    diskRead: p.dread,
    diskReadPeak: null,
    diskWrite: p.dwrite,
    diskWritePeak: null,
    cpuUser: p.cpuUser,
    cpuSystem: p.cpuSystem,
    cpuIowait: p.cpuIowait,
    cpuSteal: p.cpuSteal,
    // -1 is the store's marker for "this kernel has no PSI". It becomes null
    // here so the chart draws nothing rather than a line below the axis.
    psiCpu: p.psiCpu < 0 ? null : p.psiCpu,
    psiCpuPeak: null,
    psiMem: p.psiMem < 0 ? null : p.psiMem,
    psiMemPeak: null,
    psiIo: p.psiIo < 0 ? null : p.psiIo,
    psiIoPeak: null,
    diskReads: p.dreads,
    diskReadsPeak: null,
    diskWrites: p.dwrites,
    diskWritesPeak: null,
    diskAwait: p.await,
    diskAwaitPeak: null,
    diskBusy: p.busy,
    diskBusyPeak: null,
    tcp: p.tcp,
    tcpPeak: null,
    tcpTimeWait: null,
    // The live feed carries no load averages in its buffer — the tiles read
    // them from the newest snapshot instead, which is where "right now"
    // belongs.
    load1: null,
    load5: null,
    load15: null,
    procsBlocked: p.procsBlocked,
  }))
}

/**
 * Turns a recorded series into chart rows, inserting an explicit break
 * wherever consecutive buckets are further apart than the bucket width.
 *
 * The tolerance is generous — a bucket and a half — because a sampler that
 * drifts by a few hundred milliseconds is still running, and punching a hole
 * in the chart for that would make every graph look like an outage.
 */
export function historyRows(history: MetricsHistory): ChartRow[] {
  const step = Math.max(history.stepSeconds, 1) * 1000
  const rows: ChartRow[] = []
  let previous = 0

  for (const point of history.points) {
    const ts = new Date(point.ts).getTime()
    if (Number.isNaN(ts)) continue
    if (previous !== 0 && ts - previous > step * 1.5) rows.push(gapRow(previous + step))
    rows.push(toRow(point, ts, step))
    previous = ts
  }
  return rows
}

function toRow(point: MetricsHistoryPoint, ts: number, step: number): ChartRow {
  return {
    // Past a day the bare clock is ambiguous, so the axis carries the date too
    // once the buckets are wide enough for that to be the reading.
    t: step >= 3_600_000 ? shortDateTime(ts) : clock(point.ts),
    ts,
    at: timestamp(point.ts),
    cpu: point.cpu,
    cpuPeak: point.cpuPeak,
    mem: point.mem,
    memPeak: point.memPeak,
    swap: point.swap,
    rx: point.rx,
    rxPeak: point.rxPeak,
    tx: point.tx,
    txPeak: point.txPeak,
    disk: point.diskPercent,
    diskRead: point.diskRead,
    diskReadPeak: point.diskReadPeak,
    diskWrite: point.diskWrite,
    diskWritePeak: point.diskWritePeak,
    cpuUser: point.cpuUser,
    cpuSystem: point.cpuSystem,
    cpuIowait: point.cpuIowait,
    cpuSteal: point.cpuSteal,
    psiCpu: point.psiCpu,
    psiCpuPeak: point.psiCpuPeak,
    psiMem: point.psiMem,
    psiMemPeak: point.psiMemPeak,
    psiIo: point.psiIo,
    psiIoPeak: point.psiIoPeak,
    diskReads: point.diskReads,
    diskReadsPeak: point.diskReadsPeak,
    diskWrites: point.diskWrites,
    diskWritesPeak: point.diskWritesPeak,
    diskAwait: point.diskAwait,
    diskAwaitPeak: point.diskAwaitPeak,
    diskBusy: point.diskBusy,
    diskBusyPeak: point.diskBusyPeak,
    tcp: point.tcpConns,
    tcpPeak: point.tcpConnsPeak,
    tcpTimeWait: point.tcpTimeWait,
    load1: point.load1,
    load5: point.load5,
    load15: point.load15,
    // The run queue is a live-only reading; nothing stores it per bucket.
    procsBlocked: null,
  }
}

/**
 * A row of nothing, marking a hole in the record.
 *
 * Every field has to be listed: recharts breaks a line at a null and joins
 * across a missing key, so a series omitted here would be drawn straight
 * through the outage rather than stopping at it.
 */
function gapRow(ts: number): ChartRow {
  return {
    t: "",
    ts,
    at: "",
    cpu: null,
    cpuPeak: null,
    mem: null,
    memPeak: null,
    swap: null,
    rx: null,
    rxPeak: null,
    tx: null,
    txPeak: null,
    disk: null,
    diskRead: null,
    diskReadPeak: null,
    diskWrite: null,
    diskWritePeak: null,
    cpuUser: null,
    cpuSystem: null,
    cpuIowait: null,
    cpuSteal: null,
    psiCpu: null,
    psiCpuPeak: null,
    psiMem: null,
    psiMemPeak: null,
    psiIo: null,
    psiIoPeak: null,
    diskReads: null,
    diskReadsPeak: null,
    diskWrites: null,
    diskWritesPeak: null,
    diskAwait: null,
    diskAwaitPeak: null,
    diskBusy: null,
    diskBusyPeak: null,
    tcp: null,
    tcpPeak: null,
    tcpTimeWait: null,
    load1: null,
    load5: null,
    load15: null,
    procsBlocked: null,
  }
}

function shortDateTime(ts: number): string {
  return new Date(ts).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  })
}

/**
 * The frame shared by every recorded series — the host's and a container's
 * alike. Both endpoints return the same envelope, so the notes below read it
 * structurally rather than caring which series they were handed.
 */
export type HistoryWindow = {
  from: string
  earliest: string | null
  stepSeconds: number
  retentionSeconds: number
}

/**
 * How much of the requested window the record actually covers.
 *
 * A dashboard installed this morning cannot answer a question about last
 * Tuesday, and the honest response is to say so — an empty left-hand half of a
 * 7d chart otherwise reads as a week of zero load.
 */
export function coverageNote(history: HistoryWindow | undefined): string | null {
  if (!history || !history.earliest) return null
  const earliest = new Date(history.earliest).getTime()
  if (Number.isNaN(earliest)) return null
  const from = new Date(history.from).getTime()
  // A minute of slack: the first sample is always a little after the window
  // opens, and reporting that as incomplete coverage would be pedantic.
  if (earliest <= from + 60_000) return null
  return `recorded from ${timestamp(history.earliest)}`
}

/** The retention ceiling, phrased for the range that ran into it. */
export function retentionNote(history: HistoryWindow | undefined, spec: RangeSpec): string | null {
  if (!history) return null
  if (history.retentionSeconds >= spec.seconds) return null
  return `history is kept for ${Math.round(history.retentionSeconds / 3600)}h`
}

/** One row of a container's chart data, with the same gap semantics as the host rows. */
export type ContainerRow = {
  t: string
  ts: number
  at: string
  cpu: number | null
  cpuPeak: number | null
  mem: number | null
  memPeak: number | null
  /** Bytes per second, differenced from Docker's cumulative counters. */
  netRx: number | null
  netTx: number | null
  blockRead: number | null
  blockWrite: number | null
  pids: number | null
}

export function containerRows(history: ContainerHistory): ContainerRow[] {
  const step = Math.max(history.stepSeconds, 1) * 1000
  const rows: ContainerRow[] = []
  let previous = 0

  for (const point of history.points) {
    const ts = new Date(point.ts).getTime()
    if (Number.isNaN(ts)) continue
    // A container that was stopped and started again leaves a real hole here,
    // and it is worth seeing as a hole: the flat line a chart would otherwise
    // draw across it says the container was idle when it was not running.
    if (previous !== 0 && ts - previous > step * 1.5) rows.push(containerGapRow(previous + step))
    rows.push(toContainerRow(point, ts, step))
    previous = ts
  }
  return rows
}

function toContainerRow(point: ContainerHistoryPoint, ts: number, step: number): ContainerRow {
  return {
    t: step >= 3_600_000 ? shortDateTime(ts) : clock(point.ts),
    ts,
    at: timestamp(point.ts),
    cpu: point.cpu,
    cpuPeak: point.cpuPeak,
    mem: point.memBytes,
    memPeak: point.memBytesPeak,
    netRx: point.netRx,
    netTx: point.netTx,
    blockRead: point.blockRead,
    blockWrite: point.blockWrite,
    pids: point.pids,
  }
}

function containerGapRow(ts: number): ContainerRow {
  return {
    t: "",
    ts,
    at: "",
    cpu: null,
    cpuPeak: null,
    mem: null,
    memPeak: null,
    netRx: null,
    netTx: null,
    blockRead: null,
    blockWrite: null,
    pids: null,
  }
}

/** The memory ceiling to draw the limit line at, or 0 when the container has none. */
export function memoryLimit(history: ContainerHistory | undefined): number {
  if (!history) return 0
  let limit = 0
  for (const p of history.points) {
    if (p.memLimit > limit) limit = p.memLimit
  }
  return limit
}

/**
 * Chart rows for the per-filesystem capacity chart.
 *
 * The series are dynamic — one per mount, discovered from the data — so the
 * rows are keyed by a synthetic `m0`, `m1`… rather than by the mountpoint
 * itself. Recharts resolves a dataKey through a lodash-style path lookup, so a
 * real mountpoint containing a dot ("/mnt/data.disk") would be read as a
 * nested field and silently plot nothing.
 */
export type StorageRow = Record<string, number | string | null>

export type StorageSeriesMeta = {
  key: string
  mountpoint: string
  color: string
}

/** The palette the rest of the dashboard's charts draw from, cycled per mount. */
const MOUNT_COLORS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
]

function mountMeta(mountpoints: string[]): StorageSeriesMeta[] {
  return mountpoints.map((mountpoint, i) => ({
    key: `m${i}`,
    mountpoint,
    color: MOUNT_COLORS[i % MOUNT_COLORS.length],
  }))
}

export function storageRows(history: StorageHistory): {
  rows: StorageRow[]
  series: StorageSeriesMeta[]
} {
  const series = mountMeta(history.mounts.map((m) => m.mountpoint))
  const step = Math.max(history.stepSeconds, 1) * 1000

  // Buckets are shared across mounts because every mount is sampled at the
  // same instant, so the rows are assembled by timestamp rather than zipped by
  // index — a mount that appeared halfway through the window (a disk attached
  // on Tuesday) has fewer points than its neighbours.
  const byTs = new Map<number, StorageRow>()
  for (const [i, mount] of history.mounts.entries()) {
    const key = series[i].key
    for (const point of mount.points) {
      const ts = new Date(point.ts).getTime()
      if (Number.isNaN(ts)) continue
      let row = byTs.get(ts)
      if (!row) {
        row = {
          t: step >= 3_600_000 ? shortDateTime(ts) : clock(point.ts),
          ts,
          at: timestamp(point.ts),
        }
        byTs.set(ts, row)
      }
      row[key] = point.usedPercent
      row[`${key}i`] = point.inodesPercent
    }
  }

  const ordered = [...byTs.values()].sort((a, b) => (a.ts as number) - (b.ts as number))
  const rows: StorageRow[] = []
  let previous = 0
  for (const row of ordered) {
    const ts = row.ts as number
    if (previous !== 0 && ts - previous > step * 1.5) {
      rows.push({ t: "", ts: previous + step, at: "" })
    }
    rows.push(row)
    previous = ts
  }
  return { rows, series }
}

/** The same rows from the live feed, which carries every mount on each frame. */
export function liveStorageRows(
  history: MetricsPoint[],
  mounts: MountStats[],
): { rows: StorageRow[]; series: StorageSeriesMeta[] } {
  // The live buffer keeps only the fullest figure per frame, not a breakdown,
  // so there is one series here and it is labelled for what it is.
  const series: StorageSeriesMeta[] = [
    {
      key: "m0",
      mountpoint: mounts.length === 1 ? mounts[0].mountpoint : "fullest",
      color: MOUNT_COLORS[0],
    },
  ]
  const rows = history.map<StorageRow>((p) => ({
    t: p.t,
    ts: p.ts,
    at: new Date(p.ts).toLocaleString(),
    m0: p.disk,
  }))
  return { rows, series }
}
