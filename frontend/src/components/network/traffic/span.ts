/**
 * The window the Traffic page's charts reach back over, and how it is written
 * in the address. It was page state: a reload dropped a typed range, and a
 * container's page, which reads the same window, had no way to be told which.
 */
export type TrafficWindow = "live" | "1h" | "6h" | "24h" | "7d"
/** An explicit recorded range, in unix seconds: typed, or dragged on a chart. */
export type TrafficRange = { from: number; to: number }
export type TrafficSpan = TrafficWindow | TrafficRange
export type RecordedWindow = Exclude<TrafficWindow, "live">

export const WINDOWS: { key: TrafficWindow; label: string }[] = [
  { key: "live", label: "Live" },
  { key: "1h", label: "1h" },
  { key: "6h", label: "6h" },
  { key: "24h", label: "24h" },
  { key: "7d", label: "7d" },
]

export const WINDOW_SECONDS: Record<RecordedWindow, number> = {
  "1h": 3600,
  "6h": 6 * 3600,
  "24h": 86400,
  "7d": 7 * 86400,
}

export const WINDOW_WORDS: Record<RecordedWindow, string> = {
  "1h": "the last hour",
  "6h": "the last 6 hours",
  "24h": "the last day",
  "7d": "the last 7 days",
}

export const isRange = (span: TrafficSpan): span is TrafficRange => typeof span === "object"

export const isRecordedWindow = (value: string | null | undefined): value is RecordedWindow =>
  value !== null && value !== undefined && value in WINDOW_SECONDS

/** The span as an explicit window ending now (or at its own end), in unix seconds. */
export function spanBounds(span: TrafficSpan, now: number): TrafficRange {
  if (isRange(span)) return span
  if (span === "live") return { from: now - 15 * 60, to: now }
  return { from: now - WINDOW_SECONDS[span], to: now }
}

/** The named window that covers a span back from now, for readings that only take one. */
export function coveringWindow(span: TrafficSpan, now: number): RecordedWindow {
  if (!isRange(span)) return span === "live" ? "1h" : span
  const back = now - span.from
  return (["1h", "6h", "24h", "7d"] as const).find((w) => WINDOW_SECONDS[w] >= back) ?? "7d"
}

/**
 * The span an address asks for: `?window=24h`, or `?from=&to=` in unix
 * seconds. Anything else — an unknown window, half a range, a range that ends
 * before it starts — is the live window, which is what the page opens on.
 */
export function spanFromParams(params: { get: (name: string) => string | null }): TrafficSpan {
  const from = Number(params.get("from"))
  const to = Number(params.get("to"))
  if (
    params.get("from") &&
    params.get("to") &&
    Number.isInteger(from) &&
    Number.isInteger(to) &&
    from > 0 &&
    to > from
  ) {
    return { from, to }
  }
  const window = params.get("window")
  return isRecordedWindow(window) ? window : "live"
}

/** The span written into an address's query, the live window as nothing at all. */
export function writeSpan(params: URLSearchParams, span: TrafficSpan): URLSearchParams {
  params.delete("window")
  params.delete("from")
  params.delete("to")
  if (isRange(span)) {
    params.set("from", String(span.from))
    params.set("to", String(span.to))
  } else if (span !== "live") {
    params.set("window", span)
  }
  return params
}
