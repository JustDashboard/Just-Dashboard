/*
 * nginx's live request metrics, read from stub_status by the server every few
 * seconds while an administrator has them switched on: GET /proxy/metrics.
 */

/**
 * One reading as the overview draws it. The server's own read is left out of
 * every figure — on a quiet host it would be the only traffic there is.
 */
export type ProxyMetricsSample = {
  seq: number
  at: string
  active: number
  reading: number
  writing: number
  /** Keep-alive connections waiting for their next request. */
  waiting: number
  /**
   * Requests a second since the reading before. Null for the first reading,
   * after nginx restarted and after a gap, where no rate can honestly be drawn.
   */
  requests: number | null
  /** Connections accepted and not handled: every worker was full. */
  dropped: number
}

/** nginx's own counters since it started, the server's reads included. */
export type StubStatusTotals = {
  active: number
  accepts: number
  handled: number
  requests: number
  reading: number
  writing: number
  waiting: number
}

export type ProxyMetrics = {
  /** False where the metrics cannot be switched on, with `reason` saying why. */
  supported: boolean
  reason?: string
  /** The dashboard's status file is in place. */
  enabled: boolean
  /** A file at the status file's path that the dashboard did not write. */
  foreign?: boolean
  path: string
  /** Where the server reads the counters, e.g. http://127.0.0.1:19081/jd-status. */
  endpoint?: string
  /** Seconds between readings. */
  interval: number
  /**
   * Which series the readings belong to. It changes when the series starts
   * again, and readings held from another epoch are dropped.
   */
  epoch: number
  /** The readings after the cursor asked with, or the whole hour. */
  samples: ProxyMetricsSample[]
  current?: ProxyMetricsSample
  /** When the report was made, on the server's clock the readings are taken by. */
  at: string
  totals?: StubStatusTotals
  hourRequests: number
  hourDropped: number
  /** Why the last reading failed, and since when readings have been failing. */
  error?: string
  failingSince?: string
}
