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

/** One site's last hour in a line, as the overview's route list reads it: `1.2k req/h · 0.4% 5xx`. */
export function trafficLabel(reading: SiteTrafficReading): string {
  return `${compactCount(reading.requests)} req/h · ${share(reading.errorRate)} 5xx`
}

/** The 5xx share's tone: a few failures in a hundred is somebody's afternoon. */
export function errorRateTone(rate: number): Tone {
  if (rate >= 0.05) return "danger"
  if (rate >= 0.01) return "warning"
  return "default"
}

/**
 * Where a site's requests are read in full: its own page, which reads the
 * record its file names (an nginx site's access log, or the ingress's record
 * for a route on the Docker Caddy ingress) in the deployment Logs page's
 * workspace. The Traffic page drew a second, older copy of that record at
 * `?site=`; that address now lands on the site's page.
 */
export function readingHref(reading: Pick<SiteTrafficReading, "site">): string {
  return `/proxy/sites/${encodeURIComponent(reading.site)}`
}

/** The page's two views, as `?tab=` spells them. */
export type TrafficTab = "sites" | "errors"

/**
 * Which view an address opens. `?tab=` is the page's own; `?view=errors` is
 * what the overview's Error log link and older bookmarks say, and still opens
 * the error log.
 */
export function trafficTab(params: Pick<URLSearchParams, "get">): TrafficTab {
  const asked = params.get("tab") ?? params.get("view")
  return asked === "errors" ? "errors" : "sites"
}

/**
 * Where an old one-site address goes. `?site=<name>` opened that site's
 * requests here; they are its page's now. With `view=errors` it asked for the
 * site's error log, grouped and explained, which only this page reads, so
 * that one stays: the Errors view narrowed to the site.
 */
export function legacySiteTarget(params: Pick<URLSearchParams, "get">): string | undefined {
  const site = params.get("site")
  if (!site || trafficTab(params) === "errors") return undefined
  return readingHref({ site })
}

/** The share that failed, as the deployment pages say it: "1.0% of requests failed". */
export function failingWords(rate: number): string {
  if (rate <= 0) return "No requests failed"
  const pct = rate * 100
  return pct < 0.1 ? "Under 0.1% of requests failed" : `${percent(pct, 1)} of requests failed`
}

/** The edge's last hour as the page reads it: every logging site added up. */
export type EdgeHour = {
  /** Every site the summary named, logging or not. */
  sites: number
  logging: number
  /** Sites whose log the dashboard cannot read, or that keep none. */
  unlogged: SiteTrafficReading[]
  total: number
  /** Answered with a 5xx. */
  failed: number
  /** Answered with a 4xx, from the minutes. */
  refused: number
  bytes: number
  /** 0-1 of the hour's requests that failed. */
  rate: number
  /** Requests a minute over the hour. */
  perMinute: number
  /** Every logging site's whole log was read; otherwise the counts are floors. */
  complete: boolean
  /** The slowest site by its 19-in-20 time, where any site's log carries timing. */
  slowest?: { site: string; p95: number }
  /** How many logging sites carry timing at all. */
  timed: number
  points: TrafficPoint[]
}

/**
 * The hour across every site. The failed count is each site's share of its
 * own requests turned back into requests, since the summary carries a share
 * and not the count; rounded, so "13 of 1,284" never reads 12.9999.
 */
export function edgeHour(sites: SiteTrafficReading[]): EdgeHour {
  const logging = sites.filter((s) => s.status === "available")
  const total = logging.reduce((n, s) => n + s.requests, 0)
  const failed = Math.round(logging.reduce((n, s) => n + s.requests * s.errorRate, 0))
  const points = edgeTraffic(logging)
  const refused = points.reduce((n, p) => n + p.refused, 0)
  const timed = logging.filter((s) => typeof s.p95 === "number")
  const slowest = timed.reduce<{ site: string; p95: number } | undefined>(
    (worst, s) => (worst && worst.p95 >= (s.p95 as number) ? worst : { site: s.site, p95: s.p95! }),
    undefined,
  )
  return {
    sites: sites.length,
    logging: logging.length,
    unlogged: sites.filter((s) => s.status !== "available"),
    total,
    failed,
    refused,
    bytes: logging.reduce((n, s) => n + s.bytes, 0),
    rate: total > 0 ? failed / total : 0,
    perMinute: total / 60,
    complete: logging.every((s) => s.complete),
    slowest,
    timed: timed.length,
    points,
  }
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
    hint:
      s.status !== "available" ? "no log" : s.errorRate > 0 ? `${share(s.errorRate)} failing` : "",
    tone: s.status === "available" ? errorRateTone(s.errorRate) : "default",
    title: s.reason ?? `Open ${s.site}'s requests`,
    href: readingHref(s),
  }))
}
