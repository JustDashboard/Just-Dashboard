import type { DeploymentRequests, RequestHistory } from "@/lib/types"
import { REQUEST_RANGES, type RequestRange } from "@/lib/requests"

/**
 * The words around a window's figures, kept apart from the drawing so they
 * can be tested in a millisecond: which stretch of time the figures are
 * about, and how far back the record behind them really reaches.
 */

/** "over the last hour", "over the last 7 days", "between 18:34 and 19:06". */
export function periodWords(
  range: RequestRange,
  since: string | undefined,
  until: string | undefined,
  format: (iso: string) => string,
): string {
  if (range === "custom" && since) {
    return until ? `between ${format(since)} and ${format(until)}` : `since ${format(since)}`
  }
  const preset = REQUEST_RANGES.find((r) => r.id === range) ?? REQUEST_RANGES[1]
  return `over the ${preset.label.toLowerCase()}`
}

/** The kept history, wherever the server put it in the answer. */
export function historyOf(data: DeploymentRequests): RequestHistory | undefined {
  return data.coverage.history ?? data.history
}

/**
 * What the reader must be told about the record before trusting a figure, or
 * nothing when the window is whole.
 *
 * A window that starts before anything was kept is answered as far back as
 * the record goes, and the figures are of that shorter stretch: "7 days" of a
 * deployment created yesterday is a day. That is said with the day it starts,
 * rather than left for the reader to find in the chart's empty left half.
 * Without a kept history (a proxy site, a dashboard that has not recorded the
 * route yet), what the proxy still holds is the record, and a window reaching
 * past it is a floor.
 */
export function coverageNote(
  data: DeploymentRequests,
  since: number | undefined,
  format: (iso: string) => string,
): string | undefined {
  const history = historyOf(data)
  const from = history?.from ?? data.coverage.from
  const begins = from ? Date.parse(from) : NaN
  // A minute's grace: the window is resolved in the browser and the record's
  // first request is rarely at the window's very first second.
  const short = since !== undefined && Number.isFinite(begins) && begins > since + 60_000
  if (history?.from) {
    return short ? `Recorded since ${format(history.from)}` : undefined
  }
  if (!data.complete && data.coverage.from) {
    return `Held since ${format(data.coverage.from)}, so the figures are a floor`
  }
  if (history && !history.recording) return "Not recorded past what the proxy holds"
  return undefined
}

/** How long the dashboard keeps a route's requests, in the words of the range picker. */
export function keptWords(history: RequestHistory | undefined): string | undefined {
  if (!history || history.days <= 0) return undefined
  return history.days === 1 ? "Kept for a day" : `Kept for ${history.days} days`
}
