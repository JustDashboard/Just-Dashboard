import type { RequestEntry, RequestSummary } from "@/lib/types"
import type { Tone } from "@/components/tone"

/**
 * The vocabulary of a request log, in one place.
 *
 * A request log is read by status family, not by status code: nobody scans a
 * page for 418, they scan it for "is anything 5xx". The families are the chips,
 * the chart's stack and the colour of the code in the row, and they have to be
 * the same four colours in all three or the chart stops explaining the rows.
 */
export const STATUS_CLASSES = ["5xx", "4xx", "3xx", "2xx", "1xx", "other"] as const
export type StatusClass = (typeof STATUS_CLASSES)[number]

export function statusClass(status: number): StatusClass {
  if (status >= 500) return "5xx"
  if (status >= 400) return "4xx"
  if (status >= 300) return "3xx"
  if (status >= 200) return "2xx"
  if (status >= 100) return "1xx"
  return "other"
}

export const CLASS_LABEL: Record<StatusClass, string> = {
  "5xx": "server error",
  "4xx": "client error",
  "3xx": "redirect",
  "2xx": "ok",
  "1xx": "informational",
  other: "unreadable",
}

/**
 * What each family means, one hover away. "4xx" is not self-explanatory to
 * somebody looking at their own deployment for the first time, and the useful
 * part is whose problem it is.
 */
export const CLASS_HINT: Record<StatusClass, string> = {
  "5xx": "The deployment failed to answer. This one is yours.",
  "4xx": "The deployment refused: not found, not allowed, not authenticated. Usually the caller's.",
  "3xx": "Sent somewhere else — a redirect, or a cache saying nothing changed.",
  "2xx": "Served.",
  "1xx": "A handshake rather than an answer: an upgrade to WebSocket, or a continue.",
  other: "A line whose status could not be read.",
}

/**
 * The tone a family takes. Only the two that mean something is wrong take a
 * colour: a page where every row is tinted is a page with no signal in it, and
 * 2xx is the resting state of a working deployment.
 */
export const CLASS_TONE: Record<StatusClass, Tone> = {
  "5xx": "danger",
  "4xx": "warning",
  "3xx": "default",
  "2xx": "default",
  "1xx": "default",
  other: "default",
}

/** The status code's own colour in a row. 2xx is ink; a page of green is noise. */
export const CLASS_TEXT: Record<StatusClass, string> = {
  "5xx": "text-destructive",
  "4xx": "text-warning",
  "3xx": "text-muted-foreground",
  "2xx": "text-foreground",
  "1xx": "text-muted-foreground",
  other: "text-muted-foreground",
}

/**
 * The swatch: the chart's stack, the chip's dot and the legend, one colour
 * each. Only the two families that mean something went wrong take a hue — the
 * rest are steps of ink, exactly as the log histogram beside them is, because
 * brand blue is a command's face or a location mark and a chart of served
 * requests is neither.
 */
export const CLASS_DOT: Record<StatusClass, string> = {
  "5xx": "bg-destructive",
  "4xx": "bg-warning",
  "3xx": "bg-foreground/30",
  // Deliberately faint. On a working deployment 2xx is ninety-five per cent of
  // every column, and at a mid ink the chart was a wall of grey with the
  // thirteen-request red sliver — the only thing anyone is looking for —
  // invisible on top of it. The column's *height* is the volume reading; its
  // colour is reserved for what went wrong.
  "2xx": "bg-foreground/18",
  "1xx": "bg-muted-foreground/25",
  other: "bg-muted-foreground/20",
}

/** The rule down the left of a row. Only what needs finding draws one. */
export const CLASS_EDGE: Record<StatusClass, string> = {
  "5xx": "bg-destructive",
  "4xx": "bg-warning/70",
  "3xx": "bg-transparent",
  "2xx": "bg-transparent",
  "1xx": "bg-transparent",
  other: "bg-transparent",
}

/**
 * A method is a word, drawn as one.
 *
 * The registry blocks that do this draw a filled chip per verb in seven
 * colours, which turns a column of GETs into a column of coloured rectangles —
 * the loudest thing on a page whose actual signal is the status. Here the
 * method is monospace like the path it introduces, and only the ones that
 * change something step forward in weight.
 */
export function methodEmphasis(method: string): string {
  switch (method.toUpperCase()) {
    case "GET":
    case "HEAD":
    case "OPTIONS":
      return "text-muted-foreground"
    case "DELETE":
      return "text-destructive/90 font-medium"
    default:
      return "text-foreground font-medium"
  }
}

/**
 * A duration, at the precision a reader can act on.
 *
 * Sub-millisecond is noise from a cache hit and prints as "<1ms"; past a second
 * the tenths are what matter and the units stop. A column whose precision
 * changes per row cannot be read down, so each band has exactly one shape.
 */
export function latency(ms: number | undefined | null): string {
  if (ms === undefined || ms === null) return "—"
  if (ms < 1) return "<1ms"
  if (ms < 1000) return `${Math.round(ms)}ms`
  if (ms < 10_000) return `${(ms / 1000).toFixed(2)}s`
  return `${(ms / 1000).toFixed(1)}s`
}

/**
 * How slow this request was against the window it sits in, 0–1, for the bar
 * behind the figure. Scaled against p99 rather than the maximum: one 30-second
 * timeout would otherwise flatten every other row's bar to nothing, which is
 * the row you were looking for disappearing because of the row beside it.
 */
export function latencyShare(ms: number | undefined, summary: RequestSummary): number {
  if (ms === undefined || !summary.latency) return 0
  const ceiling = Math.max(summary.latency.p99, 1)
  return Math.min(ms / ceiling, 1)
}

/** A rate, kept honest at the low end: "0.4/min" is a truer reading than "0/min". */
export function perMinute(value: number): string {
  if (value <= 0) return "0"
  if (value >= 100) return Math.round(value).toLocaleString()
  if (value >= 10) return value.toFixed(1)
  return value.toFixed(2)
}

/** The full URI as it was requested, for the row's detail and for copying. */
export function requestURI(entry: RequestEntry): string {
  return entry.query ? `${entry.path}?${entry.query}` : entry.path
}

/**
 * A stable identity for a row, so a live prepend does not remount the list.
 * The server's sequence is the identity where there is one; the composite is
 * for rows from a record too old to carry it.
 */
export function requestKey(entry: RequestEntry, index: number): string {
  if (entry.seq) return `s${entry.seq}`
  return `${entry.time}:${entry.method}:${entry.status}:${entry.path}:${index}`
}

export const REQUEST_RANGES = [
  { id: "15m", label: "Last 15 minutes", minutes: 15 },
  { id: "1h", label: "Last hour", minutes: 60 },
  { id: "6h", label: "Last 6 hours", minutes: 360 },
  { id: "24h", label: "Last 24 hours", minutes: 1440 },
  { id: "7d", label: "Last 7 days", minutes: 10080 },
] as const

/**
 * `custom` is not a preset: it is what the window becomes when the reader drags
 * a span out of the chart, and it carries its own two instants rather than a
 * duration back from now.
 */
export type RequestRange = (typeof REQUEST_RANGES)[number]["id"] | "custom"

/**
 * Presets resolve against *now* on every read rather than being pinned when
 * chosen. "Last hour" that quietly means an hour ending twenty minutes ago is
 * the kind of wrongness nobody notices until it has cost them an afternoon.
 */
export function resolveRequestRange(range: RequestRange): string {
  const preset = REQUEST_RANGES.find((r) => r.id === range) ?? REQUEST_RANGES[1]
  return new Date(Date.now() - preset.minutes * 60_000).toISOString()
}

/**
 * What a traffic alert watches, in words. The vocabulary is the server's
 * (`TrafficAlertKinds`); the sentences are the form's, so a rule reads as a
 * sentence in the list rather than as three fields.
 */
export const ALERT_KINDS = {
  error_rate: {
    label: "Failing requests",
    unit: "%",
    hint: "The share of requests answered 5xx over the window.",
    describe: (threshold: number, minutes: number) =>
      `more than ${threshold}% of requests fail over ${minutes} min`,
    read: (observed: number) => `${observed.toFixed(1)}% failing`,
  },
  latency: {
    label: "Slow responses",
    unit: "ms",
    hint: "The p95 response time over the window.",
    describe: (threshold: number, minutes: number) =>
      `p95 above ${latency(threshold)} over ${minutes} min`,
    read: (observed: number) => `p95 ${latency(observed)}`,
  },
  silence: {
    label: "No traffic",
    unit: "",
    hint: "A site that was busy and receives nothing for the window.",
    describe: (_threshold: number, minutes: number) => `no requests for ${minutes} min`,
    read: (observed: number) => `${observed} requests`,
  },
} as const

export type AlertKind = keyof typeof ALERT_KINDS
