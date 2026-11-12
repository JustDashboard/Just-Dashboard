const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB", "PB"]

export function bytes(value: number | undefined | null, precision = 1): string {
  if (value === undefined || value === null || Number.isNaN(value)) return "—"
  // Below one byte the logarithm goes negative and there is no unit down there.
  if (Math.abs(value) < 1) return `${Math.round(value)} B`
  const exp = Math.min(
    Math.floor(Math.log(Math.abs(value)) / Math.log(1024)),
    BYTE_UNITS.length - 1,
  )
  const scaled = value / Math.pow(1024, exp)
  return `${scaled.toFixed(exp === 0 ? 0 : precision)} ${BYTE_UNITS[exp]}`
}

/**
 * `bytes` as a number and its unit, for a figure that animates the number and
 * not the unit. Below one byte it stays in bytes rather than reaching for a
 * unit smaller than the ladder has.
 */
export function byteParts(value: number): { value: number; unit: string; decimals: number } {
  if (value < 1) return { value: Math.max(value, 0), unit: "B", decimals: 0 }
  const exp = Math.min(Math.floor(Math.log(value) / Math.log(1024)), BYTE_UNITS.length - 1)
  return { value: value / Math.pow(1024, exp), unit: BYTE_UNITS[exp], decimals: exp === 0 ? 0 : 1 }
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

/**
 * A date format that is built once and then only asked.
 *
 * `toLocaleString` and its two siblings build a fresh `Intl.DateTimeFormat`
 * on every call that carries options, and building one costs about thirty
 * times what asking it does (75 µs against 2 µs). A chart's axis asks for
 * every tick of every render and a log asks for every row, which made that
 * construction the largest single cost on the Metrics page while nobody
 * touched it. So a date is printed through one of these, declared once at
 * the top of its module, and `eslint.config.mjs` refuses the calls it
 * replaces.
 *
 * The format is built on first use, in the reader's own locale and zone, and
 * built again if the zone's offset has moved under an open tab, as a laptop's
 * does when it lands somewhere else: the calls this replaces followed the
 * zone, and a clock that kept the old one would be wrong without looking it.
 * A date that is not one reads "Invalid Date", as it did, where
 * `Intl.DateTimeFormat` alone would throw.
 *
 * `toLocaleTimeString` with no time field named fills in hour, minute and
 * second; `Intl.DateTimeFormat` given the same options fills in a date. So a
 * clock names its three fields (`CLOCK`), which is what the call resolved to.
 */
export function dateFormatter(
  options: Intl.DateTimeFormatOptions,
  locale?: string,
): (date: Date | number) => string {
  let built: Intl.DateTimeFormat | undefined
  let offset = Number.NaN
  return (date) => {
    const time = typeof date === "number" ? date : date.getTime()
    // Past the last instant a Date can hold, and NaN, which fails every comparison.
    if (!(Math.abs(time) <= 8.64e15)) return "Invalid Date"
    const now = new Date().getTimezoneOffset()
    if (!built || now !== offset) {
      built = new Intl.DateTimeFormat(locale, options)
      offset = now
    }
    return built.format(time)
  }
}

/** What `toLocaleTimeString(undefined, { hour12: false })` resolves to. */
export const CLOCK: Intl.DateTimeFormatOptions = {
  hour: "numeric",
  minute: "numeric",
  second: "numeric",
  hour12: false,
}

const timestampFormat = dateFormatter({
  year: "numeric",
  month: "short",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
})
const calendarDateFormat = dateFormatter({ year: "numeric", month: "short", day: "numeric" })

/*
 * The same shapes for a caller that holds a Date or epoch milliseconds: a
 * chart's tick, an hour a feed groups by. A shape only one module prints is
 * declared there, with `dateFormatter`.
 */

/** "11:46:09" */
export const clockAt = dateFormatter(CLOCK)
/** "11:46" */
export const minuteAt = dateFormatter({ hour: "2-digit", minute: "2-digit", hour12: false })
/** "Oct 31" */
export const dayAt = dateFormatter({ month: "short", day: "numeric" })
/** "Wed, Oct 28" */
export const weekdayAt = dateFormatter({ weekday: "short", month: "short", day: "numeric" })
/** "Oct 31, 11:46" */
export const dayMinuteAt = dateFormatter({
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
})

export function timestamp(iso: string | undefined | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "—"
  return timestampFormat(d)
}

/** The day alone — for when something happened, not the second it did. */
export function calendarDate(iso: string | undefined | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "—"
  return calendarDateFormat(d)
}

/** Zero-padded clock time, for dense log and metric rows. */
export function clock(iso: string | undefined | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "—"
  return clockAt(d)
}

/** Hour and minute: a chart's axis, and a span dragged out of one. */
export function clockMinute(iso: string | undefined | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "—"
  return minuteAt(d)
}

/**
 * A span at the minute: "11:46–11:51" inside one day, and each end with its
 * date once the span crosses midnight or runs past a day — a drag on a week's
 * chart read "11:41–09:12", which is two times and no way to tell which days.
 * An open end is "now".
 */
export function minuteSpan(since: string, until?: string): string {
  const from = new Date(since)
  const to = until ? new Date(until) : undefined
  const dated =
    to !== undefined &&
    (from.toDateString() !== to.toDateString() || to.getTime() - from.getTime() > 86_400_000)
  const at = dated ? dayMinuteAt : minuteAt
  return `${at(from)}–${to ? at(to) : "now"}`
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

/**
 * A message from the API with the capital a headline starts with.
 *
 * The API words its errors to sit inside other text: lower case, no full stop
 * ("not found", "too many requests, slow down"). Standing alone as the title of
 * an error box or the reason under a toast they read as something that leaked.
 * Only a plain word is raised: a message that opens on a program's name, a
 * path, a unit or an identifier ("nginx: [emerg] …", "pg_dump: …", "eth0 is
 * down"), or that is a tool's own line with the command before a colon ("nft
 * list set: permission denied"), is somebody else's text and stays as written.
 * So does a command that is the subject of the sentence ("nginx refuses the
 * configuration", "gh did not answer": `COMMANDS`) and a command line ("wget
 * --spider http://…"): "Gh" and "Nginx" are nobody's spelling.
 */
export function sentenceCase(text: string): string {
  const trimmed = text.trim()
  const word = /^[a-z]+(?=[\s,]|$)/.exec(trimmed)?.[0]
  if (!word || COMMANDS.has(word)) return trimmed
  // Up to three lower-case words and a colon: `tar: …`, `nft list set: …`.
  const toolLine = /^[a-z0-9_.\-/]+( [a-z0-9_.\-/]+){0,2}: /.test(trimmed)
  // The word and then an option: a command line, quoted as it was run.
  const commandLine = /^[a-z]+ --?[a-z]/.test(trimmed)
  return toolLine || commandLine ? trimmed : trimmed.charAt(0).toUpperCase() + trimmed.slice(1)
}

/**
 * Commands the API's messages open on and that are written in lower case
 * wherever they stand. Only names that are nothing else: a product with a
 * capital of its own ("Docker", "Git", "Postgres") and a word that is also
 * English ("ping", "node", "mount") take the capital like any other.
 */
const COMMANDS = new Set([
  "nginx",
  "systemd",
  "systemctl",
  "journalctl",
  "logrotate",
  "crontab",
  "sysctl",
  "sudo",
  "ssh",
  "sshd",
  "gh",
  "ufw",
  "nft",
  "iptables",
  "ip",
  "tc",
  "wg",
  "conntrack",
  "ethtool",
  "resolvectl",
  "nmcli",
  "tracepath",
  "traceroute",
  "mtr",
  "wget",
  "curl",
  "rsync",
  "openssl",
  "lsblk",
  "dpkg",
  "apt",
  "dnf",
  "yum",
  "apk",
  "pacman",
  "zypper",
  "psql",
  "pgbouncer",
  "mongod",
  "mongosh",
  "mysqld",
])

/**
 * `sentenceCase` with the full stop a sentence ends on, for a reason that
 * stands on its own line. The stop is added only after a word: after a path, a
 * line number or a quoted value it would read as part of what was quoted, and
 * tool output that runs over several lines is left alone.
 */
export function asSentence(text: string): string {
  const cased = sentenceCase(text)
  // The last thing said is a word, not an address or a path that ends on a
  // letter ("…/healthz", "/etc/hosts").
  const endsOnWord = /(^|\s)[A-Za-z][A-Za-z'’-]*$/.test(cased)
  return endsOnWord && !cased.includes("\n") ? `${cased}.` : cased
}
