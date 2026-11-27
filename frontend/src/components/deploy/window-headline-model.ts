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
 * past it is a floor. With one, the server still says `complete: false` when
 * a stretch inside the window is known to be missing — a prune, a file that
 * rolled or was truncated before it was read, history and memory not meeting
 * — and that is a floor too, whatever "Kept for 30 days" would say.
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
    if (short && !data.complete) {
      return `Recorded since ${format(history.from)}, with a stretch missing, so the figures are a floor`
    }
    if (short) return `Recorded since ${format(history.from)}`
    return data.complete ? undefined : HOLE
  }
  if (!data.complete && data.coverage.from) {
    return `Held since ${format(data.coverage.from)}, so the figures are a floor`
  }
  if (history && !history.recording) return "Not recorded past what the proxy holds"
  return undefined
}

const HOLE = "Part of this window was not recorded, so the figures are a floor"

/**
 * Why a window that is not complete is a floor, for the line under the rows.
 * Without a kept history the proxy's files are the record and what rolled off
 * is before what is held; with one, the missing stretch is inside the window,
 * and "earlier requests have rolled off" would name the wrong part of it.
 */
export function floorWords(
  data: DeploymentRequests,
  format: (iso: string) => string,
): string | undefined {
  if (data.complete || !data.coverage.from) return undefined
  if (historyOf(data)?.from) return `${HOLE}.`
  return `What is held begins ${format(data.coverage.from)}; earlier requests have rolled off, so the figures are a floor.`
}

/** How long the dashboard keeps a route's requests, in the words of the range picker. */
export function keptWords(history: RequestHistory | undefined): string | undefined {
  if (!history || history.days <= 0) return undefined
  return history.days === 1 ? "Kept for a day" : `Kept for ${history.days} days`
}
