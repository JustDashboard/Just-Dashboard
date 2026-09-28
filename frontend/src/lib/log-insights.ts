import type { LogFields, LogFilterState } from "@/components/logs/types"
import type { LogBucket, LogSearchResult } from "@/lib/types"
import { fieldPredicates, fieldsOf, parsePredicate, type LogLevel } from "@/lib/log-filter"
import { STACK_LENS, type LensGroup, type LensReading, type LogLens } from "@/lib/log-lenses"

/*
 * What Insights and the readings ask the server, and how their answers are
 * read back — the questions as data, so the parts that are easy to get
 * subtly wrong (a group's predicates on top of the reader's, one search
 * serving five tiles) are tested without a browser.
 */

/** The most keys one search may rank, and the most values a split histogram keeps apart. */
export const MAX_FACETS = 12
const MAX_SERIES = 12

/**
 * The lines both questions keep, as one set of predicates — or null when
 * no line could pass both, and there is nothing to ask.
 *
 * Concatenating the two would be wrong exactly where it matters: the exact
 * values of one key are alternatives, so "event is auth_failed" from the
 * reader and "event is slow" from a group would become "either", and the
 * group would rank lines the reader filtered out. Where both sides choose
 * among values, the values left are the ones both allow; a negation, a
 * range or a presence test is a condition either way, and holds as well.
 * Two substring choices on one key cannot be intersected in this grammar,
 * so the group's own stands — wider than exact, never emptier.
 */
export function andFields(reader: LogFields, group: LogFields): LogFields | null {
  const out: LogFields = { ...reader }
  for (const [key, values] of Object.entries(group)) {
    const mine = out[key]
    if (!mine) {
      out[key] = values
      continue
    }
    const choices = (list: string[]) => list.filter(isChoice)
    const conditions = (list: string[]) => list.filter((value) => !isChoice(value))
    const a = choices(mine)
    const b = choices(values)
    let chosen: string[]
    if (a.length === 0 || b.length === 0) chosen = [...a, ...b]
    else if (a.every(isExact)) chosen = a.filter((value) => holds(exactOf(value), b))
    else if (b.every(isExact)) chosen = b.filter((value) => holds(exactOf(value), a))
    else chosen = b
    if (a.length > 0 && b.length > 0 && chosen.length === 0) return null
    out[key] = unique([...chosen, ...conditions(mine), ...conditions(values)])
  }
  return out
}

function isChoice(raw: string) {
  const op = parsePredicate(raw)?.op
  return op === "eq" || op === "has"
}

function isExact(raw: string) {
  return parsePredicate(raw)?.op === "eq"
}

function exactOf(raw: string) {
  const p = parsePredicate(raw)
  return p && p.op === "eq" ? p.value : raw
}

/** Whether a literal passes one side's alternatives, as the server would test it. */
function holds(value: string, alternatives: string[]) {
  const folded = value.toLowerCase()
  return alternatives.some((raw) => {
    const p = parsePredicate(raw)
    if (!p || !("value" in p) || typeof p.value !== "string") return false
    const want = p.value.toLowerCase()
    return p.op === "eq" ? folded === want : folded.includes(want)
  })
}

function unique(values: string[]) {
  return [...new Set(values)]
}

/** The levels both allow; an empty list is every level, and an empty meeting is nothing. */
export function andLevels(reader: LogLevel[], group: LogLevel[] | undefined): LogLevel[] | null {
  if (!group?.length) return reader
  if (!reader.length) return group
  const both = reader.filter((level) => group.includes(level))
  return both.length ? both : null
}

/** What a group ranks: the reader's question narrowed by the group's own. */
export function groupScope(
  group: Pick<LensGroup, "fields" | "levels">,
  filter: LogFilterState,
): LogFilterState | null {
  const fields = andFields(fieldsOf(filter), group.fields ?? {})
  const levels = andLevels(filter.levels, group.levels)
  if (!fields || !levels) return null
  return { ...filter, fields, levels }
}

/**
 * The keys the overview ranks: the lens's own, the level for the chips'
 * counts, and the patterns the lines fall into — which any log has, lens or
 * not, and which is how forty thousand lines become the dozen things that
 * happened. The server takes twelve.
 */
export function overviewFacets(lens: LogLens | undefined): string[] {
  return unique([...(lens?.facets ?? []), "level", "pattern"]).slice(0, MAX_FACETS)
}

/**
 * What the overview's chart is split by: what the lens names each line, or
 * for a stack — whose containers name their lines in different vocabularies
 * — which service spoke. A plain log keeps the levels.
 */
export function overviewSplit(lens: LogLens | undefined): string | undefined {
  if (!lens) return undefined
  return lens.id === STACK_LENS ? "service" : "event"
}

/**
 * A pattern as a search: its literal parts escaped for RE2, each `<*>` any
 * run of text. The server cut the pattern from the line's message, which is
 * inside its text, so the expression finds the lines it came from.
 */
export function patternRegex(pattern: string): string {
  return pattern
    .split("<*>")
    .map((part) => part.replace(/[\\^$.|?*+()[\]{}]/g, "\\$&"))
    .join(".*?")
}

/** A reading's question, as the filter a press on its tile narrows to. */
export function readingFilter(filter: LogFilterState, reading: LensReading): LogFilterState {
  return { ...filter, fields: reading.fields ?? {}, levels: reading.levels ?? [] }
}

/*
 * Readings.
 *
 * A lens has four or five, and a search per tile would be five scans of the
 * same window. Most are counts of one key's values — events, a status class
 * — so the readings on one key share a search that ranks that key and splits
 * its histogram by the values they count; the ones on levels alone share
 * another; a distinct count, and anything mixing keys and levels, asks on its
 * own. Postgres's five are two searches, the auth log's five are two.
 */

type Params = Record<string, string | string[] | number | undefined>

export type ReadingRead =
  | { id: string; kind: "values"; key: string; values: string[] }
  | { id: string; kind: "matched" }
  | { id: string; kind: "distinct"; key: string }

export type ReadingSearch = { params: Params; reads: ReadingRead[] }

export type ReadingFigure = {
  value: number
  /** The distinct count stopped being exact. */
  capped?: boolean
  /** Per histogram column; absent where the column is not what the figure counts. */
  series?: number[]
}

/** A key's values that are plain literals (or `*`), which a facet can count. */
function plainValues(reading: LensReading): { key: string; values: string[] } | undefined {
  if (reading.distinct || reading.levels?.length) return undefined
  const entries = Object.entries(reading.fields ?? {})
  if (entries.length !== 1) return undefined
  const [key, values] = entries[0]
  const plain = values.every((value) => value === "*" || isExact(value))
  return plain && values.length > 0 ? { key, values } : undefined
}

export function readingSearches(readings: LensReading[]): ReadingSearch[] {
  const keyed = new Map<string, { reading: LensReading; values: string[] }[]>()
  const leveled = new Map<string, LensReading[]>()
  const own: ReadingSearch[] = []

  for (const reading of readings) {
    const plain = plainValues(reading)
    if (plain) {
      keyed.set(plain.key, [...(keyed.get(plain.key) ?? []), { reading, values: plain.values }])
    } else if (!reading.distinct && !Object.keys(reading.fields ?? {}).length) {
      const key = [...(reading.levels ?? [])].sort().join(",")
      leveled.set(key, [...(leveled.get(key) ?? []), reading])
    } else {
      own.push({
        params: {
          f: fieldPredicates(reading.fields ?? {}),
          levels: reading.levels?.join(",") || undefined,
          facets: reading.distinct,
          facetLimit: reading.distinct ? 1 : undefined,
          limit: 1,
        },
        reads: [
          reading.distinct
            ? { id: reading.id, kind: "distinct", key: reading.distinct }
            : { id: reading.id, kind: "matched" },
        ],
      })
    }
  }

  const out: ReadingSearch[] = []
  for (const [key, members] of keyed) {
    const present = members.some((m) => m.values.includes("*"))
    const values = uniqueFolded(members.flatMap((m) => m.values.filter((v) => v !== "*")))
    out.push({
      params: {
        // A reading of "any value" (requests a minute) takes every line
        // carrying the key, and its siblings' values are among them.
        f: present ? [`${key}:*`] : values.map((value) => `${key}:${value}`),
        facets: key,
        facetLimit: 50,
        histogramBy: key,
        histogramValues: values.slice(0, MAX_SERIES).join(",") || undefined,
        limit: 1,
      },
      reads: members.map(({ reading, values: own }) =>
        own.includes("*")
          ? { id: reading.id, kind: "matched" }
          : { id: reading.id, kind: "values", key, values: own },
      ),
    })
  }

  out.push(...own)
  for (const [levels, members] of leveled) {
    out.push({
      params: { levels: levels || undefined, limit: 1 },
      reads: members.map((reading) => ({ id: reading.id, kind: "matched" })),
    })
  }
  return out
}

function uniqueFolded(values: string[]) {
  const seen = new Set<string>()
  return values.filter((value) => {
    const folded = value.toLowerCase()
    if (seen.has(folded)) return false
    seen.add(folded)
    return true
  })
}

/** One reading's figure out of the answer to the search that served it. */
export function readingFigure(read: ReadingRead, result: LogSearchResult): ReadingFigure {
  const totals = () => result.histogram.map((bucket) => bucket.total)
  switch (read.kind) {
    case "matched":
      return { value: result.matched, series: totals() }
    case "distinct": {
      const facet = result.facets?.[read.key]
      return { value: facet?.distinct ?? 0, capped: facet?.distinctCapped }
    }
    case "values": {
      const wanted = new Set(read.values.map((value) => value.toLowerCase()))
      const facet = result.facets?.[read.key]
      const value = facet
        ? facet.values.reduce((n, v) => (wanted.has(v.value.toLowerCase()) ? n + v.count : n), 0)
        : result.matched
      return { value, series: result.histogram.map((bucket) => countOf(bucket, wanted)) }
    }
  }
}

function countOf(bucket: LogBucket, wanted: Set<string>) {
  let n = 0
  for (const [key, count] of Object.entries(bucket.counts)) {
    if (wanted.has(key.toLowerCase())) n += count
  }
  return n
}

/** The minutes each readings window spans, for a per-minute figure and a `since`. */
export const READINGS_MINUTES: Record<NonNullable<LogLens["readingsWindow"]>, number> = {
  "1h": 60,
  "24h": 1440,
  "7d": 10080,
}
