import type { CellValue, GridColumn, GridColumnKind } from "@/components/database/grid"
import type { Filter, FilterOp } from "@/components/database/data/types"

/**
 * The filter bar's vocabulary, with no React in it: what each operator is
 * called, how many values it takes, which ones suit a kind of column, and how
 * a filter is said in a chip and written in the address.
 *
 * Which operators exist at all is the server's to say — `GET /databases/drivers`
 * lists them per engine, and SQLite and SQL Server have no regular expression
 * — so nothing here decides that; it only words and orders what it is given.
 */

/** How many values an operator takes. */
export type Arity = "none" | "one" | "two" | "many"

type OperatorSpec = {
  /** The words of the operator in a chip and in the picker. */
  label: string
  /** The sign, where there is a conventional one: it reads faster than the words. */
  sign?: string
  arity: Arity
}

export const OPERATORS: Record<FilterOp, OperatorSpec> = {
  eq: { label: "is", sign: "=", arity: "one" },
  ne: { label: "is not", sign: "≠", arity: "one" },
  lt: { label: "less than", sign: "<", arity: "one" },
  lte: { label: "at most", sign: "≤", arity: "one" },
  gt: { label: "greater than", sign: ">", arity: "one" },
  gte: { label: "at least", sign: "≥", arity: "one" },
  in: { label: "is one of", arity: "many" },
  not_in: { label: "is none of", arity: "many" },
  between: { label: "is between", arity: "two" },
  contains: { label: "contains", arity: "one" },
  not_contains: { label: "does not contain", arity: "one" },
  icontains: { label: "contains, any case", arity: "one" },
  prefix: { label: "starts with", arity: "one" },
  suffix: { label: "ends with", arity: "one" },
  regex: { label: "matches the pattern", arity: "one" },
  is_null: { label: "is NULL", arity: "none" },
  not_null: { label: "is not NULL", arity: "none" },
}

const KNOWN = new Set(Object.keys(OPERATORS))

export function isFilterOp(op: unknown): op is FilterOp {
  return typeof op === "string" && KNOWN.has(op)
}

/** The server takes at most this many conditions in one browse. */
export const MAX_FILTERS = 12
/** …and at most this many members in a list. */
export const MAX_LIST_VALUES = 200

const COMPARISONS: FilterOp[] = ["eq", "ne", "gt", "gte", "lt", "lte", "between", "in", "not_in"]
const TEXT_FIRST: FilterOp[] = [
  "contains",
  "icontains",
  "eq",
  "ne",
  "prefix",
  "suffix",
  "not_contains",
  "regex",
  "in",
  "not_in",
  "gt",
  "gte",
  "lt",
  "lte",
  "between",
]
const NULL_TESTS: FilterOp[] = ["is_null", "not_null"]

/**
 * The operators offered for a column, in the order that suits what it holds:
 * a number is compared before it is searched, a text is searched first, and a
 * true-or-false is only ever one or the other. `offered` is the engine's own
 * list; an operator it lacks is never in the answer. The NULL tests are left
 * out for a column that cannot hold NULL — both would be a filter with one
 * possible result.
 */
export function operatorsFor(
  column: Pick<GridColumn, "kind" | "nullable">,
  offered: readonly string[],
): FilterOp[] {
  const has = new Set(offered)
  const order = orderFor(column.kind)
  const tests = column.nullable === false ? [] : NULL_TESTS
  return [...order, ...tests].filter((op) => has.has(op))
}

function orderFor(kind: GridColumnKind): FilterOp[] {
  switch (kind) {
    case "boolean":
      return ["eq", "ne"]
    case "enum":
      return ["eq", "ne", "in", "not_in"]
    case "number":
    case "date":
    case "time":
    case "datetime":
      return [...COMPARISONS, "contains", "prefix"]
    case "uuid":
      return ["eq", "ne", "in", "not_in", "contains", "prefix"]
    case "binary":
      return ["eq", "ne"]
    case "json":
    case "array":
      return ["contains", "icontains", "not_contains", "regex", "eq", "ne"]
    default:
      return TEXT_FIRST
  }
}

/** The values a filter carries, whichever field they sit in. */
export function filterValues(filter: Filter): string[] {
  const arity = OPERATORS[filter.op].arity
  if (arity === "none") return []
  if (arity === "one") return [filter.value ?? ""]
  if (filter.values && filter.values.length > 0) return filter.values
  return filter.value === undefined ? [] : [filter.value]
}

/**
 * A filter built from what a form holds, or the reason it cannot be one.
 *
 * An empty value is a value: `name = ""` asks for the empty string, which the
 * old filter row could not say because it dropped a condition with nothing
 * typed. A list and a range are the two shapes that can be incomplete.
 */
export function buildFilter(
  column: string,
  op: FilterOp,
  values: readonly string[],
): { filter: Filter; error?: undefined } | { filter?: undefined; error: string } {
  const arity = OPERATORS[op].arity
  if (arity === "none") return { filter: { column, op } }
  if (arity === "one") return { filter: { column, op, value: values[0] ?? "" } }
  if (arity === "two") {
    if (values.length !== 2 || values.some((value) => value === "")) {
      return { error: "A range takes two values" }
    }
    return { filter: { column, op, values: [...values] } }
  }
  if (values.length === 0) return { error: "A list takes at least one value" }
  if (values.length > MAX_LIST_VALUES) {
    return { error: `A list takes at most ${MAX_LIST_VALUES} values` }
  }
  return { filter: { column, op, values: [...values] } }
}

/**
 * A typed list as its members: one per line, or separated by commas on one
 * line. A member is trimmed; an empty one is dropped, since a list is the one
 * place the empty string has to be asked for some other way (`is ""`).
 */
export function parseList(text: string): string[] {
  const parts = text.includes("\n") ? text.split(/\r?\n/) : text.split(",")
  return parts.map((part) => part.trim()).filter((part) => part !== "")
}

/**
 * A value as a chip prints it: quoted when it is text, so `""` and `" "` can
 * be seen, and a true-or-false as the word although it travels as 1 or 0.
 */
function shown(value: string, kind: GridColumnKind): string {
  if (kind === "boolean" && (value === "1" || value === "0"))
    return value === "1" ? "true" : "false"
  if ((kind === "number" || kind === "boolean") && value !== "") return value
  const text = value.length > 40 ? `${value.slice(0, 40)}…` : value
  return `"${text}"`
}

/** One filter as a chip says it: `total_cents ≥ 5000`, `email contains "@acme"`. */
export function filterLabel(filter: Filter, kind: GridColumnKind = "text"): string {
  const spec = OPERATORS[filter.op]
  const values = filterValues(filter)
  if (spec.arity === "none") return `${filter.column} ${spec.label}`
  if (spec.arity === "two") {
    return `${filter.column} ${spec.label} ${shown(values[0] ?? "", kind)} and ${shown(values[1] ?? "", kind)}`
  }
  if (spec.arity === "many") {
    const head = values.slice(0, 3).map((value) => shown(value, kind))
    const rest = values.length - head.length
    return `${filter.column} ${spec.label} ${head.join(", ")}${rest > 0 ? ` +${rest}` : ""}`
  }
  return `${filter.column} ${spec.sign ?? spec.label} ${shown(values[0] ?? "", kind)}`
}

/** Whether two filters ask the same thing. */
export function sameFilter(a: Filter, b: Filter): boolean {
  if (a.column !== b.column || a.op !== b.op) return false
  const left = filterValues(a)
  const right = filterValues(b)
  return left.length === right.length && left.every((value, i) => value === right[i])
}

/**
 * The filters as the address and the browse route carry them: one JSON array.
 * Only the fields the server reads are written, so an address made here is
 * accepted by a server that refuses unknown ones.
 */
export function encodeFilters(filters: readonly Filter[]): string {
  if (filters.length === 0) return ""
  return JSON.stringify(
    filters.map((filter) => {
      const arity = OPERATORS[filter.op].arity
      const out: Filter = { column: filter.column, op: filter.op }
      if (arity === "one") out.value = filter.value ?? ""
      if (arity === "two" || arity === "many") out.values = filterValues(filter)
      return out
    }),
  )
}

/**
 * The filters an address states. It is typed by hand and pasted between
 * people, so whatever is not a filter is dropped rather than sent: a
 * condition with no column, an operator nobody knows, a value that is not
 * text. What the engine cannot do is left for the engine to refuse — it says
 * why better than a filter that silently went missing.
 */
export function decodeFilters(raw: string): Filter[] {
  if (!raw) return []
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return []
  }
  if (!Array.isArray(parsed)) return []
  const filters: Filter[] = []
  for (const entry of parsed) {
    if (!entry || typeof entry !== "object") continue
    const { column, op, value, values } = entry as Record<string, unknown>
    if (typeof column !== "string" || column === "" || !isFilterOp(op)) continue
    const list = Array.isArray(values) ? values.filter((v) => typeof v === "string") : undefined
    const one =
      typeof value === "string" ? value : typeof value === "number" ? String(value) : undefined
    const built = buildFilter(
      column,
      op,
      list && list.length > 0 ? list : one === undefined ? [] : [one],
    )
    if (built.filter) filters.push(built.filter)
    if (filters.length === MAX_FILTERS) break
  }
  return filters
}

/**
 * The text a cell's value is compared as, or null where it cannot be: only
 * the start of it was loaded, or it is a structure no comparison takes.
 *
 * True and false go as 1 and 0. Every engine here reads those as a boolean,
 * and not every one reads the words: MySQL compares `'true'` with its
 * `tinyint(1)` as the number 0, which finds the rows that are false.
 */
export function filterText(value: CellValue): string | null {
  if (value === null) return null
  if (typeof value === "boolean") return value ? "1" : "0"
  if (typeof value === "number") return String(value)
  if (typeof value === "string") return /… \(\d+ bytes\)$/.test(value) ? null : value
  return null
}
