/** One part of a figure that is a sum. */
export type Part = {
  key: string
  label: string
  /** 0–1 of the whole figure. */
  share: number
  /** The part's own figure, for the pointer. */
  value: string
  color: string
}

const COMPACT = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 })

/** A count in the width of a tile: every digit up to ten thousand, "16.1M" past it. */
export function compact(value: number): string {
  return Math.abs(value) < 10_000
    ? Math.round(value).toLocaleString("en-US")
    : COMPACT.format(value)
}

/** A rate a second: one decimal while it is small enough to need it. */
export function perSecond(value: number): string {
  if (value <= 0) return "0"
  if (value < 10) return value.toFixed(1).replace(/\.0$/, "")
  return compact(value)
}

/**
 * How long ago an instant was, against the page's newest reading rather than
 * the wall clock, in the one unit a tile's figure has room for: an age is
 * read at a glance, and "3h 10s ago" is two figures where one was asked for.
 */
export function ago(at: number, now: number): string {
  const seconds = (now - at) / 1000
  if (seconds < 45) return "just now"
  if (seconds < 3600) return `${Math.max(1, Math.round(seconds / 60))}m ago`
  if (seconds < 48 * 3600) return `${Math.floor(seconds / 3600)}h ago`
  return `${Math.floor(seconds / 86_400)}d ago`
}
