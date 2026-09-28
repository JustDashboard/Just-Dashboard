import { percent } from "@/lib/format"
import type { Tone } from "@/components/tone"
import type { SiteTrafficReading, SiteTrafficSummary } from "@/lib/types"
import type { BarListItem } from "@/components/bar-list"

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

/** Every site's last hour, busiest first; a site that logs nothing says why. */
export function busiestItems(
  sites: SiteTrafficSummary["sites"],
  onOpen: (site: string) => void,
  limit?: number,
): BarListItem[] {
  const ranked = [...sites].sort(
    (a, b) =>
      Number(b.status === "available") - Number(a.status === "available") ||
      b.requests - a.requests,
  )
  const top = Math.max(...ranked.map((s) => s.requests), 1)
  return ranked.slice(0, limit).map((s) => ({
    key: s.site,
    label: s.site,
    value: s.status === "available" ? `${compactCount(s.requests)}/h` : "—",
    share: s.requests / top,
    signal: s.errorRate,
    tone: errorRateTone(s.errorRate) === "danger" ? "danger" : "warning",
    hint: s.status === "available" ? `${share(s.errorRate)} 5xx` : "no log",
    title: s.reason ?? `Open ${s.site}'s traffic`,
    onClick: () => onOpen(s.site),
  }))
}
