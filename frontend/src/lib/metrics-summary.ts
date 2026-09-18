import type { ChartRowLike } from "@/components/metrics/metric-chart"

/**
 * One series over one window, reduced to the figures a headline tile has
 * room for: where it averaged, how high it got and when.
 *
 * The peak reads the peak column where a series has one, for the reason the
 * legend does — on a downsampled window the mean column's own maximum is the
 * largest *average*, which is the number that hides the spike.
 */
export type WindowStat = {
  mean: number
  peak: number
  /** When the peak happened, as epoch ms. */
  peakTs: number
  count: number
}

export function windowStat(rows: ChartRowLike[], key: string, peakKey?: string): WindowStat | null {
  let sum = 0
  let count = 0
  let peak = -Infinity
  let peakTs = 0
  for (const row of rows) {
    const value = numberAt(row, key)
    if (value === null) continue
    sum += value
    count++
    const high = peakKey ? Math.max(value, numberAt(row, peakKey) ?? value) : value
    if (high > peak) {
      peak = high
      peakTs = row.ts
    }
  }
  if (count === 0) return null
  return { mean: sum / count, peak, peakTs, count }
}

/** A stat over the sum of several series — inbound plus outbound, read plus write. */
export function windowStatSum(rows: ChartRowLike[], keys: string[]): WindowStat | null {
  let sum = 0
  let count = 0
  let peak = -Infinity
  let peakTs = 0
  for (const row of rows) {
    let total = 0
    let present = false
    for (const key of keys) {
      const value = numberAt(row, key)
      if (value === null) continue
      total += value
      present = true
    }
    if (!present) continue
    sum += total
    count++
    if (total > peak) {
      peak = total
      peakTs = row.ts
    }
  }
  if (count === 0) return null
  return { mean: sum / count, peak, peakTs, count }
}

/**
 * How much a figure moved against the same figure a window earlier, as a
 * share of the earlier one. Null where there is nothing to compare against —
 * a previous window with no samples, or a mean of zero, which no ratio can
 * be taken of honestly.
 */
export function relativeChange(current: number, previous: number): number | null {
  if (!Number.isFinite(current) || !Number.isFinite(previous) || previous === 0) return null
  return ((current - previous) / Math.abs(previous)) * 100
}

/** "+12%", "−8%", or "±0%" once the change rounds to nothing. */
export function formatDelta(change: number | null): string | null {
  if (change === null) return null
  const rounded = Math.round(change)
  if (rounded === 0) return "±0%"
  return `${rounded > 0 ? "+" : "−"}${Math.abs(rounded)}%`
}

function numberAt(row: ChartRowLike, key: string): number | null {
  const value = row[key]
  return typeof value === "number" && Number.isFinite(value) ? value : null
}
