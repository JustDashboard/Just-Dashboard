/*
 * Proxy alerts: rules over the proxy's readings that tell the deployments'
 * notification channels once when a subject starts firing and once when it
 * comes back.
 */

export type ProxyAlertKind =
  | "cert_expiring"
  | "cert_expired"
  | "renewal_failed"
  | "served_drift"
  | "engine_down"
  | "upstream_down"
  | "watch_unreachable"
  | "watch_untrusted"
  | "site_errors"

/** Each kind reads its own; the server clears the rest. */
export type ProxyAlertParams = {
  /** Expiry thresholds in days, from 21, 7, 3 and 1. */
  days?: number[]
  /** How long an upstream stays down, or a site's window. */
  minutes?: number
  /** A site's 5xx share, as a percentage. */
  threshold?: number
  /** The fewest requests in the window before a site is judged. */
  minRequests?: number
}

export type ProxyAlertRule = {
  id: number
  kind: ProxyAlertKind
  params: ProxyAlertParams
  /** The channels it tells; empty tells every enabled one. */
  channels: number[]
  enabled: boolean
  createdAt: string
  updatedAt: string
}

/** One subject a rule follows that is firing, waiting to hold, or muted. */
export type ProxyAlertSubject = {
  ruleId: number
  kind: ProxyAlertKind
  subject: string
  level: string
  label: string
  detail: string
  state: "pending" | "firing" | "quiet"
  since?: string
  notifiedAt?: string
  recoveredAt?: string
  muted: boolean
}

export type ProxyAlertEvent = {
  id: number
  ruleId: number
  kind: ProxyAlertKind
  subject: string
  level: string
  label: string
  event: "proxy.alert.firing" | "proxy.alert.recovered"
  detail: string
  delivered: number
  failed: number
  muted: boolean
  createdAt: string
}

/** GET /proxy/alerts, and the answer to POST /proxy/alerts/evaluate. */
export type ProxyAlerts = {
  rules: ProxyAlertRule[]
  subjects: ProxyAlertSubject[]
  /** The newest 100 transitions, newest first. */
  history: ProxyAlertEvent[]
  /** Absent before the first pass since the dashboard started. */
  lastPass?: string
  intervalSeconds: number
}
