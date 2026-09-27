import type { LogFields, LogFilterState, LogTimeRange } from "@/components/logs/types"
import type { Tone } from "@/components/tone"
import type { LogLine } from "@/lib/types"

/**
 * The filter vocabulary, worst first. `unknown` is not a level any log writes —
 * it is the chip for the lines the parser could not classify, which on a
 * typical host is most of them. Without it, turning on any level filter
 * silently hid every line that did not happen to contain one of a dozen
 * English words, including the continuation lines of the stack trace being
 * hunted.
 */
export const LOG_LEVELS = ["critical", "error", "warn", "info", "debug", "unknown"] as const
export type LogLevel = (typeof LOG_LEVELS)[number]

export const LEVEL_LABEL: Record<LogLevel, string> = {
  critical: "critical",
  error: "error",
  warn: "warn",
  info: "info",
  debug: "debug",
  unknown: "other",
}

/** What a line's level means, one hover away — the levels are not obvious. */
export const LEVEL_HINT: Record<LogLevel, string> = {
  critical: "The process is failing or the host is: emerg, alert, crit, fatal.",
  error: "Something did not work. err and error.",
  warn: "Working, but it will not stay that way. warn and warning.",
  info: "Normal operation. notice and info.",
  debug: "Developer detail. debug and trace.",
  unknown: "No level in the line — including the continuation lines of a stack trace.",
}

/**
 * The colour of the line's *message*. Only the two ends of the scale take one:
 * a critical line is the one that must be found across a screen of others, and
 * debug recedes. An error's message stays ink — the edge and the level mark
 * beside it are already red, and a page of red paragraphs stops being scannable
 * exactly when it matters.
 */
export const LEVEL_TEXT: Record<string, string> = {
  critical: "text-destructive",
  error: "text-foreground",
  warn: "text-foreground",
  info: "text-foreground",
  debug: "text-muted-foreground",
}

/** The rule down a line's left edge. Only the levels that need finding draw one. */
export const LEVEL_EDGE: Record<string, string> = {
  critical: "bg-destructive",
  error: "bg-destructive/70",
  warn: "bg-warning",
  info: "bg-transparent",
  debug: "bg-transparent",
}

/**
 * The word in the level column, short enough to be a column. `unknown` is
 * blank rather than "other": most of a syslog carries no level, and a column
 * that says so on every line says nothing.
 */
export const LEVEL_MARK: Record<LogLevel, string> = {
  critical: "crit",
  error: "err",
  warn: "warn",
  info: "info",
  debug: "dbg",
  unknown: "",
}

export const LEVEL_TONE: Record<LogLevel, Tone> = {
  critical: "danger",
  error: "danger",
  warn: "warning",
  info: "default",
  debug: "default",
  unknown: "default",
}

/** The dot on a level chip, the legend and the histogram's stack — one swatch per level. */
export const LEVEL_DOT: Record<LogLevel, string> = {
  critical: "bg-destructive",
  error: "bg-destructive/70",
  warn: "bg-warning",
  info: "bg-foreground/70",
  debug: "bg-muted-foreground/60",
  unknown: "bg-muted-foreground/30",
}

export const EMPTY_FILTER: LogFilterState = {
  q: "",
  exclude: "",
  regex: false,
  ignoreCase: true,
  levels: [],
  fields: {},
}

export function isFilterActive(f: LogFilterState): boolean {
  return (
    f.q !== "" || f.exclude !== "" || f.levels.length > 0 || Object.keys(fieldsOf(f)).length > 0
  )
}

/**
 * The query every log route shares. One builder means the live socket, the
 * history search and the export cannot drift apart: narrowing the stream to one
 * request id and pressing Export gives you that, not the whole file.
 *
 * Still a plain object, so every caller that spreads it keeps working; the
 * predicates ride as an array, which `buildUrl` sends as repeated `f=`, and
 * as `undefined` when there are none, so a query key built from this does not
 * change shape the moment the first field is added.
 */
export function filterQuery(f: LogFilterState) {
  return {
    q: f.q || undefined,
    exclude: f.exclude || undefined,
    regex: f.regex ? "true" : undefined,
    // The server defaults this on, so only the opt-out needs sending.
    ignoreCase: f.ignoreCase ? undefined : "false",
    levels: f.levels.length ? f.levels.join(",") : undefined,
    f: fieldPredicates(fieldsOf(f)),
  }
}

/** The same question, whatever order its levels and values were chosen in. */
export function filterEquals(a: LogFilterState, b: LogFilterState): boolean {
  return (
    a.q === b.q &&
    a.exclude === b.exclude &&
    a.regex === b.regex &&
    a.ignoreCase === b.ignoreCase &&
    sameSet(a.levels, b.levels) &&
    fieldsEqual(fieldsOf(a), fieldsOf(b))
  )
}

export function fieldsEqual(a: LogFields, b: LogFields): boolean {
  const keys = Object.keys(a)
  if (keys.length !== Object.keys(b).length) return false
  return keys.every((key) => b[key] !== undefined && sameSet(a[key], b[key]))
}

function sameSet(a: readonly string[], b: readonly string[]) {
  if (a.length !== b.length) return false
  const seen = new Set(a)
  return b.every((value) => seen.has(value))
}

/*
 * Field predicates — `f=key:value` on every log route.
 *
 * The grammar is the server's (`logsx.Filter`), and this copy has to agree
 * with it exactly: the live pane counts its quick views and facet values here,
 * over lines the server already filtered, and a count that disagrees with the
 * lines a press then fetches is the page contradicting itself. The shared
 * vectors in `backend/internal/logsx/testdata/predicates.json` are run by both
 * test suites for that reason.
 *
 * A value is one of:
 *   lit      exact, ignoring case (when it does not start with an operator)
 *   =lit     exact — the escape, for a literal that starts with one
 *   !lit     not equal
 *   *  !*    present, absent
 *   ~sub     contains, ignoring case;  !~sub  does not contain
 *   >n >=n <n <=n   numeric; a value that is not a number never matches
 *
 * Within a key, the exact and `~` forms are alternatives (any may hold); every
 * other form is a condition that must hold as well. Keys are all required.
 */

const FIELD_KEY = /^[A-Za-z0-9_.@-]{1,64}$/

/** The server refuses more than this many predicates, and longer values. */
export const MAX_FIELD_PREDICATES = 32
export const MAX_FIELD_VALUE_BYTES = 256

type Predicate =
  | { op: "eq" | "has" | "ne" | "lacks"; value: string }
  | { op: "present" | "absent" }
  | { op: "gt" | "ge" | "lt" | "le"; value: number }

const COMPARISONS = [
  [">=", "ge"],
  ["<=", "le"],
  [">", "gt"],
  ["<", "lt"],
] as const

/** Reads one value's form, or null for one the server would refuse. */
export function parsePredicate(raw: string): Predicate | null {
  if (raw === "*") return { op: "present" }
  if (raw === "!*") return { op: "absent" }
  if (raw.startsWith("!~")) return raw.length > 2 ? { op: "lacks", value: raw.slice(2) } : null
  if (raw.startsWith("!")) return raw.length > 1 ? { op: "ne", value: raw.slice(1) } : null
  if (raw.startsWith("~")) return raw.length > 1 ? { op: "has", value: raw.slice(1) } : null
  for (const [prefix, op] of COMPARISONS) {
    if (raw.startsWith(prefix)) {
      const n = parseNumber(raw.slice(prefix.length))
      return n === undefined ? null : { op, value: n }
    }
  }
  if (raw.startsWith("=")) return raw.length > 1 ? { op: "eq", value: raw.slice(1) } : null
  // `*x` is neither the wildcard nor a literal: the literal would be `=*x`.
  if (raw === "" || raw.startsWith("*")) return null
  return { op: "eq", value: raw }
}

const DECIMAL = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/

/**
 * A number as Go's `strconv.ParseFloat` reads one — the decimal forms, and the
 * infinities and NaN it accepts in any case — and undefined for anything else,
 * so `1,000` and ` 5` fail here exactly as they fail on the server.
 */
function parseNumber(text: string): number | undefined {
  if (DECIMAL.test(text)) return Number(text)
  if (/^[+-]?inf(?:inity)?$/i.test(text)) return text.startsWith("-") ? -Infinity : Infinity
  if (/^[+-]?nan$/i.test(text)) return NaN
  return undefined
}

export function validFieldKey(key: string): boolean {
  // The level has its own parameter and its own chips; the server refuses it here.
  return FIELD_KEY.test(key) && key !== "level"
}

export function validFieldValue(value: string): boolean {
  return (
    new TextEncoder().encode(value).length <= MAX_FIELD_VALUE_BYTES &&
    parsePredicate(value) !== null
  )
}

const NO_FIELDS: LogFields = Object.freeze({}) as LogFields

/**
 * The filter's predicates, and the only way anything reads them. A session
 * stored before fields existed has none, a link can carry a key the server
 * refuses, and a hand-edited value can be any shape at all; each is dropped
 * here rather than thrown on or sent. A well-formed map comes back as itself,
 * so a memo keyed on it holds.
 */
export function fieldsOf(f: Pick<LogFilterState, "fields"> | null | undefined): LogFields {
  const raw = f?.fields as unknown
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return NO_FIELDS
  const out: LogFields = {}
  let clean = true
  let count = 0
  for (const [key, values] of Object.entries(raw as Record<string, unknown>)) {
    if (!validFieldKey(key) || !Array.isArray(values)) {
      clean = false
      continue
    }
    const kept: string[] = []
    for (const value of values) {
      if (
        typeof value !== "string" ||
        !validFieldValue(value) ||
        kept.includes(value) ||
        count >= MAX_FIELD_PREDICATES
      ) {
        clean = false
        continue
      }
      kept.push(value)
      count++
    }
    if (kept.length) out[key] = kept
    else clean = false
  }
  return clean ? (raw as LogFields) : out
}

/** The predicates as the wire spells them, keys in order so a query key is stable. */
export function fieldPredicates(fields: LogFields): string[] | undefined {
  const out = Object.keys(fields)
    .sort()
    .flatMap((key) => fields[key].map((value) => `${key}:${value}`))
  return out.length ? out : undefined
}

/**
 * The `f=` values of a link, as fields: split at the first colon (an IPv6
 * address is all colons), and anything the server would refuse dropped here,
 * so a bad link opens the page on the rest of its question rather than a 400.
 */
export function fieldsFromParams(values: readonly string[]): LogFields {
  const out: LogFields = {}
  let count = 0
  for (const raw of values) {
    const at = raw.indexOf(":")
    if (at < 1 || count >= MAX_FIELD_PREDICATES) continue
    const key = raw.slice(0, at)
    const value = raw.slice(at + 1)
    if (!validFieldKey(key) || !validFieldValue(value)) continue
    if (out[key]?.includes(value)) continue
    out[key] = [...(out[key] ?? []), value]
    count++
  }
  return out
}

/** A `levels=` value from a link, keeping only the levels there are chips for. */
export function levelsFromParam(value: string | null): LogLevel[] {
  const known = new Set<string>(LOG_LEVELS)
  const out: LogLevel[] = []
  for (const part of (value ?? "").split(",")) {
    const level = part.trim().toLowerCase()
    if (known.has(level) && !out.includes(level as LogLevel)) out.push(level as LogLevel)
  }
  return out
}

/**
 * The value a key answers for this line, in the server's order (`Line.Value`):
 * the event, the stream and the source by name, the level, then what the lens
 * read out of the text, then a structured line's own fields. Empty is missing.
 */
export function lineValue(line: LogLine, key: string): string | undefined {
  switch (key) {
    case "event":
      return line.event || undefined
    case "stream":
      return line.stream || undefined
    case "source":
      return line.source || undefined
    case "level":
      return line.level || "unknown"
  }
  return line.attrs?.[key] || line.fields?.[key] || undefined
}

/** Whether a line passes the predicates, exactly as the server decides it. */
export function matchFields(line: LogLine, fields: LogFields): boolean {
  for (const [key, values] of Object.entries(fields)) {
    if (!validFieldKey(key)) continue
    const value = lineValue(line, key)
    const folded = value?.toLowerCase()
    let alternatives = false
    let anyHolds = false
    for (const raw of values) {
      const p = parsePredicate(raw)
      if (!p) continue
      switch (p.op) {
        case "eq":
          alternatives = true
          if (folded !== undefined && folded === p.value.toLowerCase()) anyHolds = true
          break
        case "has":
          alternatives = true
          if (folded !== undefined && folded.includes(p.value.toLowerCase())) anyHolds = true
          break
        case "ne":
          if (folded !== undefined && folded === p.value.toLowerCase()) return false
          break
        case "lacks":
          if (folded !== undefined && folded.includes(p.value.toLowerCase())) return false
          break
        case "present":
          if (value === undefined) return false
          break
        case "absent":
          if (value !== undefined) return false
          break
        default: {
          const n = value === undefined ? undefined : parseNumber(value)
          if (n === undefined || !compare(n, p.op, p.value)) return false
        }
      }
    }
    if (alternatives && !anyHolds) return false
  }
  return true
}

function compare(n: number, op: "gt" | "ge" | "lt" | "le", bound: number) {
  switch (op) {
    case "gt":
      return n > bound
    case "ge":
      return n >= bound
    case "lt":
      return n < bound
    case "le":
      return n <= bound
  }
}

/** A literal as an exact predicate, escaped when it starts with an operator. */
export function exactValue(value: string): string {
  return /^[!><*~=]/.test(value) ? `=${value}` : value
}

/** "Only lines where this key is this": the key's predicates become just that. */
export function onlyField(fields: LogFields, key: string, value: string): LogFields {
  return { ...fields, [key]: [exactValue(value)] }
}

/**
 * "Hide lines where this key is this": the exclusion joins the key's other
 * conditions, and an inclusion of the same value — which could now never
 * hold — goes.
 */
export function hideField(fields: LogFields, key: string, value: string): LogFields {
  const exact = exactValue(value).toLowerCase()
  const kept = (fields[key] ?? []).filter((v) => v.toLowerCase() !== exact)
  const negation = `!${value}`
  return { ...fields, [key]: kept.includes(negation) ? kept : [...kept, negation] }
}

/** A facet value pressed: included if it was not, and let go if it was. */
export function toggleField(fields: LogFields, key: string, value: string): LogFields {
  const exact = exactValue(value)
  const current = fields[key] ?? []
  const next = current.includes(exact)
    ? current.filter((v) => v !== exact)
    : [...current.filter((v) => v !== `!${value}`), exact]
  return withKey(fields, key, next)
}

export function clearField(fields: LogFields, key: string): LogFields {
  return withKey(fields, key, [])
}

function withKey(fields: LogFields, key: string, values: string[]): LogFields {
  const out = { ...fields }
  if (values.length) out[key] = values
  else delete out[key]
  return out
}

/** One predicate in words, for a chip and its accessible name: "is not root", "≥ 500". */
export function describePredicate(raw: string): string {
  const p = parsePredicate(raw)
  if (!p) return raw
  switch (p.op) {
    case "eq":
      return p.value
    case "ne":
      return `not ${p.value}`
    case "has":
      return `contains ${p.value}`
    case "lacks":
      return `lacks ${p.value}`
    case "present":
      return "any value"
    case "absent":
      return "no value"
    case "gt":
      return `> ${p.value}`
    case "ge":
      return `≥ ${p.value}`
    case "lt":
      return `< ${p.value}`
    case "le":
      return `≤ ${p.value}`
  }
}

/**
 * `key:value` words in the search box, as fields — `user:postgres slow` is
 * a field and a word. Only keys the reader could mean are taken (the lens's
 * vocabulary), so `12:30` and `http://host` stay text. A quoted value keeps
 * its spaces: `path:"/a b"`. The rest of the words are the search.
 */
export function parseFieldTokens(
  q: string,
  knownKeys: ReadonlySet<string> | readonly string[],
): { q: string; fields: LogFields } {
  const known = knownKeys instanceof Set ? knownKeys : new Set(knownKeys as readonly string[])
  const fields: LogFields = {}
  const rest: string[] = []
  for (const token of q.match(/(?:[^\s"]+|"[^"]*")+/g) ?? []) {
    const match = /^([A-Za-z0-9_.@-]{1,64}):(.+)$/.exec(token)
    const key = match?.[1]
    const value = match?.[2].replace(/^"(.*)"$/, "$1")
    if (key && value && known.has(key) && validFieldKey(key) && validFieldValue(value)) {
      if (!fields[key]?.includes(value)) fields[key] = [...(fields[key] ?? []), value]
    } else {
      rest.push(token)
    }
  }
  return { q: rest.join(" "), fields }
}

/**
 * The filter with its search box's `key:value` words moved into its fields,
 * or the same filter when it had none — so a caller can tell nothing changed.
 */
export function withFieldTokens(
  filter: LogFilterState,
  knownKeys: ReadonlySet<string> | readonly string[],
): LogFilterState {
  if (filter.regex || !filter.q.includes(":")) return filter
  const parsed = parseFieldTokens(filter.q, knownKeys)
  const added = Object.entries(parsed.fields)
  if (added.length === 0) return filter
  const fields = { ...fieldsOf(filter) }
  for (const [key, values] of added) {
    fields[key] = [...(fields[key] ?? []), ...values.filter((v) => !fields[key]?.includes(v))]
  }
  return { ...filter, q: parsed.q, fields }
}

export const TIME_RANGES: { id: LogTimeRange; label: string; minutes: number | null }[] = [
  { id: "15m", label: "Last 15 minutes", minutes: 15 },
  { id: "1h", label: "Last hour", minutes: 60 },
  { id: "6h", label: "Last 6 hours", minutes: 360 },
  { id: "24h", label: "Last 24 hours", minutes: 1440 },
  { id: "7d", label: "Last 7 days", minutes: 10080 },
  { id: "all", label: "Everything on disk", minutes: null },
  { id: "custom", label: "Custom range", minutes: null },
]

export function readLogWindow(params: Pick<URLSearchParams, "get">) {
  const since = params.get("since") ?? ""
  const until = params.get("until") ?? ""
  const valid = (value: string) =>
    !value ||
    (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,3})?(?:Z|[+-]\d{2}:\d{2})$/.test(value) &&
      Number.isFinite(Date.parse(value)))
  const error =
    !valid(since) || !valid(until) || (since && until && Date.parse(since) >= Date.parse(until))
      ? "The log link has an invalid time window. Choose a new window before searching."
      : undefined
  return { since, until, error }
}

/** Display in local time without replacing the original instant used by search. */
export function logTimeInput(value: string) {
  if (!value || !/(?:Z|[+-]\d{2}:\d{2})$/.test(value)) return value
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return ""
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 23)
}

/**
 * Resolves the window to the pair of instants the server takes. Presets are
 * relative to *now* and are re-resolved on every search rather than pinned when
 * chosen — "last hour" that quietly means an hour that ended twenty minutes ago
 * is the kind of wrongness nobody notices until it has cost them an afternoon.
 */
export function resolveRange(
  range: LogTimeRange,
  customSince: string,
  customUntil: string,
): { since?: string; until?: string } {
  if (range === "custom") {
    return {
      since: customSince ? new Date(customSince).toISOString() : undefined,
      until: customUntil ? new Date(customUntil).toISOString() : undefined,
    }
  }
  const preset = TIME_RANGES.find((r) => r.id === range)
  if (!preset?.minutes) return {}
  return { since: new Date(Date.now() - preset.minutes * 60_000).toISOString() }
}

/**
 * Where the search term sits inside a line, for the client-side case.
 *
 * The server sends byte ranges for search results because the browser cannot
 * re-run a Go regular expression faithfully. A live tail has no such answer —
 * computing ranges per line on a hot streaming path is work for lines nobody
 * will scroll back to — so highlighting there is done here, and a pattern
 * JavaScript refuses simply goes unhighlighted rather than throwing.
 */
export function highlightRanges(text: string, f: LogFilterState): [number, number][] {
  if (!f.q) return []
  let re: RegExp
  try {
    const pattern = f.regex ? f.q : f.q.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
    re = new RegExp(pattern, f.ignoreCase ? "gi" : "g")
  } catch {
    return []
  }
  const out: [number, number][] = []
  for (const m of text.matchAll(re)) {
    if (m.index === undefined || m[0].length === 0) continue
    out.push([m.index, m.index + m[0].length])
    if (out.length >= 32) break
  }
  return out
}

/** Splits a line into plain and highlighted runs, given ranges in byte order. */
export function segmentLine(text: string, ranges: [number, number][] | undefined) {
  if (!ranges?.length) return [{ text, hit: false }]
  const out: { text: string; hit: boolean }[] = []
  let at = 0
  for (const [start, end] of ranges) {
    if (start < at || start >= text.length) continue
    if (start > at) out.push({ text: text.slice(at, start), hit: false })
    out.push({ text: text.slice(start, Math.min(end, text.length)), hit: true })
    at = Math.min(end, text.length)
  }
  if (at < text.length) out.push({ text: text.slice(at), hit: false })
  return out
}
