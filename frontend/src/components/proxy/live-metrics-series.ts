import { clock } from "@/lib/format"
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
 * before the report was made drop off, as they do on the server — while
 * readings fail no newer one arrives, and the held ones still age out.
 */
export function mergeSeries(held: MetricsSeries | undefined, report: ProxyMetrics): MetricsSeries {
  const kept = held && held.epoch === report.epoch ? held.samples : []
  const seen = kept.at(-1)?.seq ?? 0
  const samples = [...kept, ...report.samples.filter((sample) => sample.seq > seen)]
  const cut = Date.parse(report.at) - HOUR_MS
  return { epoch: report.epoch, samples: samples.filter((sample) => Date.parse(sample.at) >= cut) }
}

/** A rate as a figure: tenths below ten a second, whole numbers above. */
export function perSecond(rate: number): string {
  return rate.toLocaleString(undefined, { maximumFractionDigits: rate < 10 ? 1 : 0 })
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
