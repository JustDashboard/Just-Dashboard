import { isEmptyDocument, shapeProblem } from "@/components/database/mongo/shell"
import type { MongoCount, MongoFindSpec } from "@/components/database/mongo/types"

/**
 * The query bar's fields, as text. Every one is what the reader typed — the
 * server reads Extended JSON and the shell's spelling alike — and an empty
 * one is "none".
 */
export type QueryDraft = {
  filter: string
  project: string
  sort: string
  collation: string
  hint: string
  skip: string
  limit: string
  maxTime: string
}

export const EMPTY_QUERY: QueryDraft = {
  filter: "",
  project: "",
  sort: "",
  collation: "",
  hint: "",
  skip: "",
  limit: "",
  maxTime: "",
}

export type QueryField = keyof QueryDraft

/** The fields in the order the address and the bar list them. */
export const QUERY_FIELDS: readonly QueryField[] = [
  "filter",
  "project",
  "sort",
  "collation",
  "hint",
  "skip",
  "limit",
  "maxTime",
]

/** The fields behind "Options": everything but the filter. */
export const OPTION_FIELDS: readonly QueryField[] = QUERY_FIELDS.slice(1)

export const PAGE_SIZES = [20, 50, 100, 250] as const
export const DEFAULT_PAGE = 50

/** The draft an address states. A key that is absent is an empty field. */
export function draftFromAddress(param: (name: string) => string): QueryDraft {
  const draft = { ...EMPTY_QUERY }
  for (const field of QUERY_FIELDS) draft[field] = param(field)
  return draft
}

/** The address keys of a draft: `null` for an empty field, so the key leaves the address. */
export function addressOf(draft: QueryDraft): Record<QueryField, string | null> {
  const out = {} as Record<QueryField, string | null>
  for (const field of QUERY_FIELDS) out[field] = draft[field].trim() ? draft[field] : null
  return out
}

export function sameDraft(a: QueryDraft, b: QueryDraft): boolean {
  return QUERY_FIELDS.every((field) => a[field].trim() === b[field].trim())
}

export function isEmptyDraft(draft: QueryDraft): boolean {
  return QUERY_FIELDS.every((field) => !draft[field].trim())
}

/** How many of the fields behind "Options" hold something. */
export function optionCount(draft: QueryDraft): number {
  return OPTION_FIELDS.filter((field) => draft[field].trim()).length
}

const WHOLE = /^\d+$/

/**
 * What is wrong with one field as typed, or `null`. It says only what can be
 * told without the server — a bracket never closed, a skip that is not a
 * number — and the server's own refusal, which knows the operators, is shown
 * in the same place when it comes.
 */
export function fieldProblem(field: QueryField, text: string): string | null {
  const value = text.trim()
  if (!value) return null
  switch (field) {
    case "filter":
    case "project":
    case "sort":
    case "collation":
      return shapeProblem(text, "document")
    case "hint": {
      // An index by its name, or by its key pattern.
      if (value[0] === "{") return shapeProblem(text, "document")
      return null
    }
    case "skip":
      return WHOLE.test(value) ? null : "A whole number, 0 or more."
    case "limit":
      return WHOLE.test(value) && Number(value) >= 1 ? null : "A whole number, 1 or more."
    case "maxTime":
      if (!WHOLE.test(value) || Number(value) < 1) return "Milliseconds, 1 or more."
      return Number(value) > 300_000 ? "At most 300000 ms (five minutes)." : null
  }
}

export function draftProblems(draft: QueryDraft): Partial<Record<QueryField, string>> {
  const problems: Partial<Record<QueryField, string>> = {}
  for (const field of QUERY_FIELDS) {
    const problem = fieldProblem(field, draft[field])
    if (problem) problems[field] = problem
  }
  return problems
}

/** The words of the server's fields, for a refusal that names one (`projection: line 1, …`). */
const SERVER_FIELD: Record<string, QueryField> = {
  filter: "filter",
  projection: "project",
  sort: "sort",
  collation: "collation",
  hint: "hint",
  skip: "skip",
  limit: "limit",
  maxTimeMS: "maxTime",
}

/** What a refusal from the server is about: one field of the bar, or the query as a whole. */
export type Refusal = { field: QueryField | null; message: string }

/**
 * The server's own words for a field, where its refusal names none. Each is
 * tried in turn and only blames a field that holds something: a sort that is
 * empty cannot be the sort that was refused.
 */
const SAID_OF: readonly [RegExp, QueryField][] = [
  [/\$sort\b|\bsort (?:key|order|pattern|specification)\b/i, "sort"],
  [
    /\bcollation\b|\blocale\b|'(?:strength|caseLevel|caseFirst|numericOrdering|alternate)'/i,
    "collation",
  ],
  [/\bhint\b/i, "hint"],
  [/\$project\b|\bprojection\b|\b(?:inclusion|exclusion)\b/i, "project"],
  [/time limit|MaxTimeMS/i, "maxTime"],
  [
    /unknown (?:top level )?operator|\bbad query\b|\$(?:and|or|nor)\b.*\bneed|\$in needs|\$regex\b/i,
    "filter",
  ],
]

/** The fields that hold a document, where an operator can be written. */
const DOCUMENT_FIELDS: readonly QueryField[] = ["filter", "project", "sort", "collation", "hint"]

/**
 * The field a refusal from the server is about, and the sentence without the
 * field's name in front of it.
 *
 * A refusal that begins with a field's name (`sort: line 1, …`) is that
 * field's. One that does not is read for what it names — `$sort`, a locale,
 * a hint — and, failing that, for an operator that only one field of the bar
 * contains. What is left belongs to no field: it is the query's, and is said
 * under the bar rather than pinned on the filter, which is where a refusal of
 * a sort used to land.
 */
export function refusedField(message: string, draft: QueryDraft, code?: string): Refusal {
  const match = /^([A-Za-z]+): ([\s\S]+)$/.exec(message)
  if (match && Object.hasOwn(SERVER_FIELD, match[1])) {
    return { field: SERVER_FIELD[match[1]], message: capitalised(match[2]) }
  }
  if (code === "query_timeout") {
    return { field: draft.maxTime.trim() ? "maxTime" : null, message }
  }
  for (const [said, field] of SAID_OF) {
    if (said.test(message) && draft[field].trim()) return { field, message }
  }
  const operators = message.match(/\$[A-Za-z]\w*/g) ?? []
  const holders = DOCUMENT_FIELDS.filter((field) =>
    operators.some((operator) => new RegExp(`\\${operator}\\b`).test(draft[field])),
  )
  return { field: holders.length === 1 ? holders[0] : null, message }
}

const capitalised = (text: string) => text.charAt(0).toUpperCase() + text.slice(1)

/** A page of a draft, as the find route takes it. */
export function findSpec(draft: QueryDraft, page: { skip: number; limit: number }): MongoFindSpec {
  const text = (value: string) => (value.trim() ? value : undefined)
  return {
    filter: text(draft.filter),
    projection: text(draft.project),
    sort: text(draft.sort),
    collation: text(draft.collation),
    hint: text(draft.hint),
    skip: page.skip,
    limit: page.limit,
    maxTimeMS: draft.maxTime.trim() ? Number(draft.maxTime) : undefined,
  }
}

/**
 * Which documents a page shows. The bar's Skip and Limit bound the whole
 * query — "the 200 after the first 1,000" — and the page turns inside that:
 * the reader's skip is where page one starts, and the reader's limit is where
 * the last page ends.
 */
export function pageWindow(
  draft: QueryDraft,
  page: number,
  size: number,
): { page: number; skip: number; limit: number; last: boolean; cap: number | null } {
  const base = WHOLE.test(draft.skip.trim()) ? Number(draft.skip) : 0
  const cap = limitOf(draft)
  if (cap === null) {
    return { page, skip: base + page * size, limit: size, last: false, cap }
  }
  // A page past the reader's own limit is the last page inside it.
  const at = Math.min(page, Math.max(Math.ceil(cap / size) - 1, 0))
  const left = cap - at * size
  return { page: at, skip: base + at * size, limit: Math.min(size, left), last: left <= size, cap }
}

/** The reader's own Limit, when the bar states a usable one. */
export function limitOf(draft: QueryDraft): number | null {
  const value = draft.limit.trim()
  return WHOLE.test(value) && Number(value) > 0 ? Number(value) : null
}

/** Whether the draft asks for some documents rather than all of them. */
export function isFiltered(draft: QueryDraft): boolean {
  return !isEmptyDocument(draft.filter)
}

/**
 * "1–50 of 6,000": where the page sits in what the query matches. An inexact
 * count is said as one ("of about"), and a count that is of the whole
 * collection under a filter is said as that, never as the number of matches.
 */
export function rangeWords(
  first: number,
  returned: number,
  count: MongoCount | null,
  filtered: boolean,
  cap: number | null,
): string {
  const n = (value: number) => value.toLocaleString("en-US")
  if (returned === 0) {
    if (!count) return "No documents"
    return count.value === 0 || (count.scope === "filter" && first >= count.value)
      ? first > 0
        ? `Past the last of ${n(count.value)}`
        : "No documents"
      : "No documents on this page"
  }
  const range = `${n(first + 1)}–${n(first + returned)}`
  if (!count) return range
  if (filtered && count.scope === "collection") {
    return `${range} · ${n(count.value)} in the collection`
  }
  const total = cap !== null ? Math.min(count.value, cap) : count.value
  return `${range} of ${count.exact ? "" : "about "}${n(total)}`
}

/* ----------------------------------------------------------------- history */

export type HistoryEntry = {
  database: string
  collection: string
  draft: QueryDraft
  /** ms since 1970. */
  at: number
}

/** How many queries the history keeps. */
export const HISTORY_SIZE = 30

/**
 * The history with one more query run. A query already in it moves to the
 * front rather than being listed twice, and a query with nothing in it — the
 * whole collection — is not history.
 */
export function withHistory(
  history: readonly HistoryEntry[],
  entry: HistoryEntry,
): readonly HistoryEntry[] {
  if (isEmptyDraft(entry.draft)) return history
  const rest = history.filter(
    (held) =>
      !(
        held.database === entry.database &&
        held.collection === entry.collection &&
        sameDraft(held.draft, entry.draft)
      ),
  )
  return [entry, ...rest].slice(0, HISTORY_SIZE)
}

/** One line for a query in a menu: its filter first, then what else it sets. */
export function draftLabel(draft: QueryDraft): string {
  const parts: string[] = []
  if (draft.filter.trim()) parts.push(oneLine(draft.filter))
  if (draft.project.trim()) parts.push(`project ${oneLine(draft.project)}`)
  if (draft.sort.trim()) parts.push(`sort ${oneLine(draft.sort)}`)
  if (draft.skip.trim()) parts.push(`skip ${draft.skip.trim()}`)
  if (draft.limit.trim()) parts.push(`limit ${draft.limit.trim()}`)
  if (draft.collation.trim()) parts.push("collation")
  if (draft.hint.trim()) parts.push(`hint ${oneLine(draft.hint)}`)
  return parts.join(" · ") || "{}"
}

const oneLine = (text: string) => text.trim().replace(/\s+/g, " ")

/** The query as the shell would write it, for copying and for a title. */
export function findStatement(collection: string, draft: QueryDraft): string {
  const name = /^[A-Za-z_][\w]*$/.test(collection)
    ? `db.${collection}`
    : `db.getCollection(${JSON.stringify(collection)})`
  const filter = draft.filter.trim() ? oneLine(draft.filter) : "{}"
  const project = draft.project.trim() ? `, ${oneLine(draft.project)}` : ""
  let text = `${name}.find(${filter}${project})`
  if (draft.sort.trim()) text += `.sort(${oneLine(draft.sort)})`
  if (draft.collation.trim()) text += `.collation(${oneLine(draft.collation)})`
  if (draft.hint.trim()) text += `.hint(${oneLine(draft.hint)})`
  if (draft.skip.trim()) text += `.skip(${draft.skip.trim()})`
  if (draft.limit.trim()) text += `.limit(${draft.limit.trim()})`
  if (draft.maxTime.trim()) text += `.maxTimeMS(${draft.maxTime.trim()})`
  return text
}

/* ------------------------------------------------------------------ memory */

/** What the Documents page last asked of each collection, kept for the tab: Schema's "find these" builds on it. */
export const queryMemoryKey = (id: number) => `databases.${id}.mongo.query`

/** A collection among a server's, as one string: its database and its name. */
export const collectionKey = (database: string, collection: string) =>
  `${database}\u0000${collection}`
