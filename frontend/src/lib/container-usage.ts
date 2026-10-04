import type { ContainerNetworkCounters, ContainerStats } from "@/lib/types"
import { clock, rate, timestamp } from "@/lib/format"
import { containerGapRow, type ContainerRow } from "@/lib/metrics-range"

export const CONTAINER_STALE_MS = 10_000

/** How far back a live chart reaches: five minutes of Docker's one frame a second. */
export const LIVE_WINDOW_MS = 5 * 60_000

export function containerRateLabel(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value) || value < 0) return "—"
  // Sparse traffic can average below one byte/s; the general byte formatter
  // assumes whole bytes and cannot label this interval accurately.
  if (value > 0 && value < 1) return `${value.toPrecision(2)} B/s`
  return rate(value)
}

/** Docker timestamps, not message arrival times: a slow connection can batch frames. */
export function containerRates(current: ContainerStats, previous?: ContainerStats) {
  const elapsed = previous ? (Date.parse(current.ts) - Date.parse(previous.ts)) / 1000 : 0
  const continuous =
    previous !== undefined &&
    current.id === previous.id &&
    elapsed > 0 &&
    elapsed <= CONTAINER_STALE_MS / 1000 &&
    current.cpuTotal >= previous.cpuTotal &&
    current.systemCpu >= previous.systemCpu
  const delta = (now: number | undefined, before: number | undefined) =>
    continuous &&
    typeof now === "number" &&
    typeof before === "number" &&
    Number.isFinite(now) &&
    Number.isFinite(before) &&
    now >= before
      ? (now - before) / elapsed
      : null

  const interfaces = Object.entries(current.networks ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, counters]) => {
      const prior = previous?.networks?.[name]
      const rateOf = (key: keyof ContainerNetworkCounters) => delta(counters[key], prior?.[key])
      return {
        name,
        ...counters,
        rx: rateOf("rxBytes"),
        tx: rateOf("txBytes"),
        rxPacketsRate: rateOf("rxPackets"),
        txPacketsRate: rateOf("txPackets"),
      }
    })
  // An interface arriving or disappearing changes the scope of the total.
  // Wait one interval instead of presenting a partial total as complete.
  const sameInterfaces =
    current.networkAvailable === true &&
    previous?.networkAvailable === true &&
    interfaces.length > 0 &&
    interfaces.length === Object.keys(previous.networks ?? {}).length
  const sum = (key: "rx" | "tx") =>
    sameInterfaces && interfaces.every((entry) => entry[key] !== null)
      ? interfaces.reduce((total, entry) => total + (entry[key] ?? 0), 0)
      : null
  const periods = delta(current.cpuPeriods, previous?.cpuPeriods)
  const throttled = delta(current.cpuThrottledPeriods, previous?.cpuThrottledPeriods)
  return {
    interfaces,
    rx: sum("rx"),
    tx: sum("tx"),
    read:
      current.blockAvailable && previous?.blockAvailable
        ? delta(current.blockRead, previous.blockRead)
        : null,
    write:
      current.blockAvailable && previous?.blockAvailable
        ? delta(current.blockWrite, previous.blockWrite)
        : null,
    throttledPercent:
      periods !== null && periods > 0 && throttled !== null && throttled <= periods
        ? (throttled / periods) * 100
        : null,
  }
}

/**
 * One frame of the stats socket as a chart row, its rates measured against the
 * frame before it. Everything `containerRates` cannot vouch for — the first
 * frame, a reset counter, an interface that came or went — is a null, which the
 * chart draws as a break rather than as a spike or a fall to zero.
 */
export function liveRow(current: ContainerStats, previous?: ContainerStats): ContainerRow {
  const rates = containerRates(current, previous)
  return {
    t: clock(current.ts),
    ts: Date.parse(current.ts),
    at: timestamp(current.ts),
    cpu: current.cpuReady ? current.cpuPercent : null,
    cpuPeak: null,
    mem: current.memUsage,
    memPeak: null,
    netRx: rates.rx,
    netTx: rates.tx,
    blockRead: rates.read,
    blockWrite: rates.write,
    pids: current.pids,
  }
}

/**
 * The live window with one more frame on it. A frame that does not follow on
 * from the last one — a socket that stalled, a tab that was away — opens a gap
 * rather than drawing a line across time nobody measured, and rows older than
 * the window fall off the front. A frame no newer than the last row is a repeat
 * and changes nothing, so two readers of one container cannot draw it twice.
 */
export function appendLive(
  rows: ContainerRow[],
  current: ContainerStats,
  previous?: ContainerStats,
): ContainerRow[] {
  const row = liveRow(current, previous)
  const last = rows.at(-1)
  if (!Number.isFinite(row.ts) || (last && row.ts <= last.ts)) return rows
  const from = row.ts - LIVE_WINDOW_MS
  const kept = rows.filter((one) => one.ts >= from)
  if (kept.length > 0 && last && row.ts - last.ts > CONTAINER_STALE_MS) {
    kept.push(containerGapRow(last.ts + 1000))
  }
  kept.push(row)
  return kept
}

/** An axis's extent and the ticks that name it. */
export type Scale = { domain: [number, number]; ticks: number[] }

// Steps within one unit whose quarters all print as a whole number of that
// unit or the one below it: 12 MB ticks at 3, 6, 9 and 12; 3 MB at 768 KB.
const BYTE_STEPS = [1, 2, 3, 4, 6, 8, 12, 16, 24, 32, 48, 64, 96, 128, 192, 256, 384, 512, 768]
// Percent tops whose quarters need at most two decimals: 1% ticks at 0.25%.
const CPU_TOPS = [1, 2, 4, 8, 10, 20, 40, 60, 80, 100]

function quarters(top: number): Scale {
  return { domain: [0, top], ticks: [0, top / 4, top / 2, (top * 3) / 4, top] }
}

/**
 * A byte or byte-rate axis that ends on a round figure above the peak.
 *
 * Fitted to the data, recharts split a 3.1 MB/s peak into 781.3 KB/s steps —
 * a scale nobody can read a value off — and an idle interface's zero-height
 * series into ticks of fractions of a byte. The headroom keeps the peak off
 * the top rule, where it read as a line clipped by the frame.
 */
export function byteScale(peak: number): Scale {
  const wanted = Math.max(peak * 1.08, 1024)
  for (let unit = 1; ; unit *= 1024) {
    const step = BYTE_STEPS.find((one) => one * unit >= wanted)
    if (step !== undefined) return quarters(step * unit)
  }
}

/**
 * The processor axis: at least 1%, so an idle container's hundredths of a
 * percent draw as the near-flat line they are rather than as peaks on a scale
 * whose every tick rounds to "0%"; past 100%, the next hundred, because 100%
 * is one core and a container may use several.
 */
export function cpuScale(peak: number): Scale {
  const wanted = Math.max(peak * 1.08, 1)
  return quarters(CPU_TOPS.find((top) => top >= wanted) ?? Math.ceil(wanted / 100) * 100)
}

/** The highest value any of `keys` reaches across `rows`, peaks included. */
export function peakOf<T extends object>(rows: T[], keys: (keyof T)[]): number {
  let peak = 0
  for (const row of rows) {
    for (const key of keys) {
      const value = row[key]
      if (typeof value === "number" && value > peak) peak = value
    }
  }
  return peak
}
