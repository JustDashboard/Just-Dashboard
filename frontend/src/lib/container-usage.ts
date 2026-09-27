import type { ContainerNetworkCounters, ContainerStats } from "@/lib/types"
import { rate } from "@/lib/format"

export const CONTAINER_STALE_MS = 10_000

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
