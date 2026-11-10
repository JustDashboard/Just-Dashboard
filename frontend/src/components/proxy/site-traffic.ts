import { percent } from "@/lib/format"
import type { Tone } from "@/components/tone"
import type { SiteTrafficReading, SiteTrafficSummary, TrafficPoint } from "@/lib/types"

const COMPACT = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 })

/** A request count as a route row reads it: 1.2k rather than 1,187. */
export function compactCount(n: number): string {
  return COMPACT.format(n).toLowerCase()
}

/** A share of 0–1 as the percentage the readings print. */
export function share(rate: number): string {
  return percent(rate * 100, rate > 0 && rate < 0.001 ? 2 : 1)
}

/** One site's last hour in a line: `1.2k req/h · 0.4% 5xx`. */
export function trafficLabel(reading: SiteTrafficReading): string {
  return `${compactCount(reading.requests)} req/h · ${share(reading.errorRate)} 5xx`
}

/** The 5xx share's tone: a few failures in a hundred is somebody's afternoon. */
export function errorRateTone(rate: number): Tone {
  if (rate >= 0.05) return "danger"
  if (rate >= 0.01) return "warning"
  return "default"
}

export type TrafficView = "requests" | "errors"

/** Where a site's traffic opens; the contract other proxy pages link by. */
export function trafficHref(site: string, view?: TrafficView) {
  const q = new URLSearchParams({ site })
  if (view === "errors") q.set("view", "errors")
  return `/proxy/traffic?${q}`
}

/**
 * Where a reading's requests are read in full. An nginx site's on the Traffic
 * page, which reads the file its configuration names; a route on the Docker
 * Caddy ingress has no such file, so its own site page, which reads the
 * deployment's record.
 */
export function readingHref(reading: Pick<SiteTrafficReading, "site" | "engine">): string {
  return reading.engine === "caddy-ingress"
    ? `/proxy/sites/${encodeURIComponent(reading.site)}`
    : trafficHref(reading.site)
}

/** The edge's hour: every site's minutes added up, oldest first. */
export function edgeTraffic(sites: SiteTrafficReading[]): TrafficPoint[] {
  const byMinute = new Map<string, TrafficPoint>()
  for (const site of sites) {
    for (const p of site.points ?? []) {
      const at = byMinute.get(p.start) ?? {
        start: p.start,
        total: 0,
        refused: 0,
        failed: 0,
        bytes: 0,
      }
      at.total += p.total
      at.refused += p.refused
      at.failed += p.failed
      at.bytes += p.bytes
      byMinute.set(p.start, at)
    }
  }
  return [...byMinute.values()].sort((a, b) => Date.parse(a.start) - Date.parse(b.start))
}

/** One site's row of the busiest list: its hour, and how much of it failed. */
export type BusiestSite = {
  key: string
  label: string
  value: string
  /** 0–1 of every site's requests in the hour; absent when nothing was served. */
  share?: number
  hint: string
  /** The failing share's tone, for the hint's colour. */
  tone: Tone
  title: string
  href: string
}

/**
 * Every site's last hour, busiest first; a site that logs nothing says why.
 * A row's share is of the edge's whole hour, every site added up, so the
 * column says how the traffic is split rather than how each compares with the
 * busiest.
 */
export function busiestSites(sites: SiteTrafficSummary["sites"], limit?: number): BusiestSite[] {
  const ranked = [...sites].sort(
    (a, b) =>
      Number(b.status === "available") - Number(a.status === "available") ||
      b.requests - a.requests,
  )
  const whole = ranked.reduce((sum, s) => sum + (s.status === "available" ? s.requests : 0), 0)
  return ranked.slice(0, limit).map((s) => ({
    key: s.site,
    label: s.site,
    value: s.status === "available" ? `${compactCount(s.requests)}/h` : "—",
    share: s.status === "available" && whole > 0 ? s.requests / whole : undefined,
    hint: s.status === "available" ? `${share(s.errorRate)} 5xx` : "no log",
    tone: s.status === "available" ? errorRateTone(s.errorRate) : "default",
    title: s.reason ?? `Open ${s.site}'s traffic`,
    href: readingHref(s),
  }))
}
