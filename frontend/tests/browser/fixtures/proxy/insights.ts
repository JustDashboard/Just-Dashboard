import { json, type ProxyRoutes } from "./shared"

/**
 * The engine's live request metrics, GET/PUT /proxy/metrics. The default host
 * has them switched off; the showcase has an hour of readings.
 */

export const STATUS_FILE = "/etc/nginx/conf.d/jd-status.conf"
export const STATUS_ENDPOINT = "http://127.0.0.1:19081/jd-status"

export type MetricsSample = {
  seq: number
  at: string
  active: number
  reading: number
  writing: number
  waiting: number
  requests: number | null
  dropped: number
}

/** n readings five seconds apart ending now, with a rate that climbs. */
export function metricSeries(n: number, first = 1): MetricsSample[] {
  const end = Date.now()
  return Array.from({ length: n }, (_, i) => ({
    seq: first + i,
    at: new Date(end - (n - 1 - i) * 5_000).toISOString(),
    active: 20 + (i % 12),
    reading: 1,
    writing: 3 + (i % 4),
    waiting: 16 + (i % 8),
    requests: i === 0 ? null : 8 + (i % 30) / 2,
    dropped: 0,
  }))
}

export function metricsOff(extra: Record<string, unknown> = {}) {
  return {
    supported: true,
    enabled: false,
    path: STATUS_FILE,
    interval: 5,
    epoch: 1,
    at: new Date().toISOString(),
    samples: [],
    hourRequests: 0,
    hourDropped: 0,
    ...extra,
  }
}

export function metricsOn(samples: MetricsSample[], extra: Record<string, unknown> = {}) {
  return {
    ...metricsOff(),
    enabled: true,
    endpoint: STATUS_ENDPOINT,
    epoch: 1_790_000_000_000,
    samples,
    current: samples.at(-1),
    totals: {
      active: 29,
      accepts: 184_220,
      handled: 184_220,
      requests: 402_113,
      reading: 1,
      writing: 5,
      waiting: 23,
    },
    hourRequests: 36_914,
    hourDropped: 0,
    ...extra,
  }
}

export const routes: ProxyRoutes = {
  "/proxy/metrics": (route) => json(route, metricsOff()),
}

export const showcase: ProxyRoutes = {
  "/proxy/metrics": (route) => json(route, metricsOn(metricSeries(720))),
}
