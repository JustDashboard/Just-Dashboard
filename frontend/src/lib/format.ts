export function bytes(value: number | undefined | null, precision = 1): string {
  if (value === undefined || value === null || Number.isNaN(value)) return "—"
  if (value === 0) return "0 B"
  const units = ["B", "KB", "MB", "GB", "TB", "PB"]
  const exp = Math.min(Math.floor(Math.log(Math.abs(value)) / Math.log(1024)), units.length - 1)
  const scaled = value / Math.pow(1024, exp)
  return `${scaled.toFixed(exp === 0 ? 0 : precision)} ${units[exp]}`
}

export function rate(bytesPerSecond: number | undefined | null): string {
  if (!bytesPerSecond) return "0 B/s"
  return `${bytes(bytesPerSecond)}/s`
}

export function percent(value: number | undefined | null, precision = 1): string {
  if (value === undefined || value === null || Number.isNaN(value)) return "—"
  return `${value.toFixed(precision)}%`
}

/** Compact duration for uptimes and ages: 3d 4h, 12m 5s. */
export function duration(seconds: number | undefined | null): string {
  if (seconds === undefined || seconds === null || seconds < 0) return "—"
  const s = Math.floor(seconds)
  if (s < 60) return `${s}s`
  const units: [number, string][] = [
    [86400, "d"],
    [3600, "h"],
    [60, "m"],
    [1, "s"],
  ]
  const parts: string[] = []
  let rest = s
  for (const [size, label] of units) {
    const n = Math.floor(rest / size)
    if (n > 0) {
      parts.push(`${n}${label}`)
      rest -= n * size
    }
    if (parts.length === 2) break
  }
  return parts.join(" ")
}

export function relativeTime(iso: string | undefined | null): string {
  if (!iso) return "—"
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return "—"
  const delta = (Date.now() - then) / 1000
  if (Math.abs(delta) < 45) return "just now"
  const suffix = delta > 0 ? "ago" : "from now"
  return `${duration(Math.abs(delta))} ${suffix}`
}

export function timestamp(iso: string | undefined | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "—"
  return d.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })
}

/** The day alone — for when something happened, not the second it did. */
export function calendarDate(iso: string | undefined | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "—"
  return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" })
}

/** Zero-padded clock time, for dense log and metric rows. */
export function clock(iso: string | undefined | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "—"
  return d.toLocaleTimeString(undefined, { hour12: false })
}

export function shortSha(sha: string | undefined | null, length = 7): string {
  if (!sha) return "—"
  return sha.slice(0, length)
}

export function truncateMiddle(value: string, max = 48): string {
  if (value.length <= max) return value
  const half = Math.floor((max - 1) / 2)
  return `${value.slice(0, half)}…${value.slice(value.length - half)}`
}

/**
 * A number and its noun, agreeing.
 *
 * Written out at each site this was right about half the time — "3 matches
 * across 2 tables" next to "Delete 1 rows" in a confirmation dialog, which is
 * the worst place in the product to look careless. The plural is the regular
 * one unless a second form is given, because the irregulars here are few
 * ("index", "matches") and naming them at the call site is clearer than a
 * table of exceptions.
 */
export function plural(n: number, singular: string, many?: string): string {
  return `${n.toLocaleString()} ${n === 1 ? singular : (many ?? singular + "s")}`
}
