import type { RequestEntry, RequestLatency, RequestSummary } from "@/lib/types"
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

/**
 * A status code's colour, wherever a code is drawn: the request rows, their
 * detail, the facets, and a status inside a line of the host's log console
 * (`components/logs/log-text.tsx` reads it from here). One map, so a 404 is
 * the same amber in both consoles.
 *
 * Every family takes its hue, 2xx included. A column of codes then scans as
 * all green (§11), and the 500 and the 404 in it are found by colour rather
 * than by reading — which ink-on-ink for 2xx and 3xx never allowed. 3xx takes
 * the path hue, not a status hue: a redirect is a direction, not a verdict.
 */
export const CLASS_TEXT: Record<StatusClass, string> = {
  "5xx": "text-destructive",
  "4xx": "text-warning",
  "3xx": "text-[var(--tag-cyan)]",
  "2xx": "text-success",
  "1xx": "text-muted-foreground",
  other: "text-muted-foreground",
}

/**
 * The chip's dot, in the code's own colour, so the chip is the legend for the
 * codes in the rows it filters. The chart names its area separately — it is
 * one series of requests, not a stack by family.
 */
export const CLASS_DOT: Record<StatusClass, string> = {
  "5xx": "bg-destructive",
  "4xx": "bg-warning",
  "3xx": "bg-[var(--tag-cyan)]",
  "2xx": "bg-success",
  "1xx": "bg-muted-foreground/60",
  other: "bg-muted-foreground/40",
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
 * method is monospace like the path it introduces, reads stay muted, and the
 * ones that change something take the class a method has inside a log line
 * (`log-text.tsx`'s method token), so a POST reads the same in both consoles.
 *
 * DELETE is one of them and nothing more. It used to borrow the danger hue,
 * and a DELETE answered 204 is not a failure: the status says whether it went
 * wrong.
 */
export function methodEmphasis(method: string): string {
  switch (method.toUpperCase()) {
    case "GET":
    case "HEAD":
    case "OPTIONS":
      return "text-muted-foreground"
    case "POST":
    case "PUT":
    case "PATCH":
    case "DELETE":
      return "font-semibold text-[var(--tag-blue)]"
    default:
      return "text-foreground font-medium"
  }
}

/**
 * What a status code says, in the words a reader uses for it: "404 Not
 * found". Only the codes a deployment actually answers with; anything else is
 * its family's word from `CLASS_LABEL`, which is never wrong.
 */
const STATUS_WORDS: Record<number, string> = {
  101: "Switching protocols",
  200: "OK",
  201: "Created",
  202: "Accepted",
  204: "No content",
  206: "Partial content",
  301: "Moved",
  302: "Found",
  303: "See other",
  304: "Not modified",
  307: "Redirect",
  308: "Redirect",
  400: "Bad request",
  401: "Unauthorized",
  403: "Forbidden",
  404: "Not found",
  405: "Method not allowed",
  408: "Timeout",
  409: "Conflict",
  410: "Gone",
  413: "Too large",
  422: "Unprocessable",
  429: "Too many",
  // nginx's own code for a caller that hung up before the answer.
  499: "Client closed",
  500: "Server error",
  502: "Bad gateway",
  503: "Unavailable",
  504: "Gateway timeout",
}

export function statusWord(code: number): string {
  const word = STATUS_WORDS[code] ?? CLASS_LABEL[statusClass(code)]
  return word === "ok" ? "OK" : word.charAt(0).toUpperCase() + word.slice(1)
}

/**
 * The line past which a response is slow: a second, the same line the
 * Slowest tenth tile draws. Below it a figure is a reading; above it the
 * figure is what the reader came to find.
 */
export function latencyTone(ms: number | undefined): Tone {
  return ms !== undefined && ms > 1000 ? "warning" : "default"
}

/**
 * Where one request sits among the window's, in words: "in the slowest
 * hundredth". A figure alone asks the reader to remember the percentiles; the
 * bracket answers whether 412ms is this deployment being slow or this
 * deployment being itself. Each bracket is the percentile its words name —
 * the slowest tenth begins at the p90 — because a request between the p90
 * and the p95 is in the slowest tenth, and saying "slower than most" of it
 * was the words being wrong about the number beside them.
 */
export function latencyBracket(
  ms: number | undefined,
  latency: RequestLatency | undefined,
): string | undefined {
  if (ms === undefined || !latency) return undefined
  if (ms > latency.p99) return "in the slowest hundredth"
  if (ms > latency.p90) return "in the slowest tenth"
  if (ms > latency.p75) return "slower than most"
  if (ms >= latency.p50) return "about the median"
  return "faster than the median"
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
