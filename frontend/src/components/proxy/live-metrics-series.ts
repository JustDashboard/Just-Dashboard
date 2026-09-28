import { clock, clockMinute } from "@/lib/format"
import type { ProxyMetrics, ProxyMetricsSample } from "@/lib/proxy/types-metrics"

/**
 * The readings the overview holds, and the series they belong to. The server
 * keeps the hour; the page asks only for what it lacks every five seconds, so
 * a poll carries one reading rather than seven hundred and twenty.
 */
export type MetricsSeries = { epoch: number; samples: ProxyMetricsSample[] }

const HOUR_MS = 3_600_000

/** What the next poll asks for: the readings after the newest one held. */
export function metricsCursor(held: MetricsSeries | undefined) {
  const newest = held?.samples.at(-1)
  return held && newest ? { epoch: held.epoch, after: newest.seq } : undefined
}

/**
 * Folds a report into what is held. Another epoch is another series — the
 * metrics switched off and on, the port moved, the dashboard restarted — and
 * replaces it; the same epoch adds what is new. Readings older than an hour
 * before the newest drop off, as they do on the server.
 */
export function mergeSeries(held: MetricsSeries | undefined, report: ProxyMetrics): MetricsSeries {
  const kept = held && held.epoch === report.epoch ? held.samples : []
  const seen = kept.at(-1)?.seq ?? 0
  const samples = [...kept, ...report.samples.filter((sample) => sample.seq > seen)]
  const newest = samples.at(-1)
  if (!newest) return { epoch: report.epoch, samples }
  const cut = Date.parse(newest.at) - HOUR_MS
  return { epoch: report.epoch, samples: samples.filter((sample) => Date.parse(sample.at) >= cut) }
}

/**
 * The request rate's shape. A reading with no rate is left out rather than
 * drawn at zero: after a restart nginx served something, just not a number
 * that can be told.
 */
export function requestTrend(samples: ProxyMetricsSample[]): number[] {
  return samples.flatMap((sample) => (sample.requests === null ? [] : [sample.requests]))
}

export function connectionTrend(samples: ProxyMetricsSample[]): number[] {
  return samples.map((sample) => sample.active)
}

/** A rate as a figure: tenths below ten a second, whole numbers above. */
export function perSecond(rate: number): string {
  return rate.toLocaleString(undefined, { maximumFractionDigits: rate < 10 ? 1 : 0 })
}

/**
 * How long the held readings reach back, for the hour's total: "in the last
 * hour" once they span it, and from when they start until then — right after
 * switching on, an hour's total would claim an hour nobody measured.
 */
export function windowLabel(samples: ProxyMetricsSample[]): string {
  const oldest = samples[0]
  const newest = samples.at(-1)
  if (!oldest || !newest) return ""
  if (Date.parse(newest.at) - Date.parse(oldest.at) >= HOUR_MS - 60_000) return "in the last hour"
  return `since ${clockMinute(oldest.at)}`
}

/** The connections' three states, in nginx's words for the first two. */
export function connectionStates(sample: ProxyMetricsSample): string {
  return `${sample.reading.toLocaleString()} reading · ${sample.writing.toLocaleString()} writing · ${sample.waiting.toLocaleString()} idle`
}

/**
 * The address the server reads, as the reader would type it: the endpoint
 * without its scheme.
 */
export function statusAddress(report: ProxyMetrics): string {
  return (report.endpoint ?? "").replace(/^http:\/\//, "")
}

/** What the footnote says when the readings have stopped. */
export function readingsStopped(report: ProxyMetrics): string | undefined {
  if (!report.error) return undefined
  const since = report.failingSince ? ` since ${clock(report.failingSince)}` : ""
  return `No answer${since}: ${report.error}`
}
