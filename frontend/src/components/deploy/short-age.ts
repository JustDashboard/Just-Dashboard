/**
 * How long ago, in the one unit a state column has room for: "3m", "2h",
 * "5d". `relativeTime` says "3m 12s ago", which is right in a sentence and
 * too long beside a dot.
 */
export function shortAge(iso: string | undefined, now = Date.now()): string {
  const then = iso ? Date.parse(iso) : NaN
  if (Number.isNaN(then)) return ""
  const seconds = Math.max(0, Math.floor((now - then) / 1000))
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  if (seconds < 172800) return `${Math.floor(seconds / 3600)}h`
  return `${Math.floor(seconds / 86400)}d`
}
