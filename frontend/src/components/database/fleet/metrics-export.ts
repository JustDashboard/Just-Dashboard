import { bytes, plural } from "@/lib/format"
import type { DbMetricsExport } from "@/lib/types"

/**
 * A Prometheus scrape job for this dashboard's database metrics, as it would be
 * pasted into `prometheus.yml`. The scheme and host are the page's own origin,
 * because that is the one address known to reach the dashboard; the key is the
 * one just made, or a placeholder when none is on screen.
 */
export function scrapeConfig(origin: string, endpoint: string, key = "<metrics key>") {
  const url = new URL(origin)
  return [
    "scrape_configs:",
    "  - job_name: just-dashboard-databases",
    `    scheme: ${url.protocol.replace(":", "")}`,
    `    metrics_path: ${endpoint}`,
    "    authorization:",
    `      credentials: ${key}`,
    "    static_configs:",
    `      - targets: ["${url.host}"]`,
  ].join("\n")
}

/** Choosing a shorter window deletes the history that falls outside it. */
export function shortens(report: Pick<DbMetricsExport, "retentionDays">, days: number) {
  return days < report.retentionDays
}

/** What the history weighs now, said once, in the words the retention rests on. */
export function storedSentence(report: DbMetricsExport) {
  if (report.storedBytes === null) return "Nothing is recorded yet."
  return `About ${bytes(report.storedBytes)} in ${plural(report.samples, "saved reading")} so far.`
}

/**
 * What a window would weigh, from the average reading the recorder has saved
 * and the servers it has read in the last day. `null` until there is something
 * to measure by, which the page says rather than inventing a figure.
 */
export function windowEstimate(report: DbMetricsExport, days: number) {
  return report.options.find((option) => option.days === days)?.estimatedBytes ?? null
}

/** The rule the retention follows, in the server's own numbers. */
export function keepingSentence(report: Pick<DbMetricsExport, "rawDays" | "coarseMinutes">) {
  return `Readings are kept whole for ${plural(report.rawDays, "day")}. Older ones are thinned to the last of every ${report.coarseMinutes} minutes.`
}
