import type { GridSort } from "@/components/database/grid"
import { decodeFilters, encodeFilters } from "@/components/database/data/filters"
import type { BrowsePage, Filter, SortKey } from "@/components/database/data/types"

/**
 * How one table is being looked at, as its address states it.
 *
 * The page, the sort, the filters and which of the three views is open are in
 * the query string beside the table's name, so a link to "the unpaid orders,
 * newest first" opens exactly that for whoever is handed it, and Back goes to
 * the table the reader came from with its own view. They used to live in one
 * session slot per connection, tagged with a single table: not shareable, and
 * forgotten the moment another table was opened.
 *
 * The page size is not here. It is how the reader likes the grid arranged, on
 * this screen, for every table — furniture, kept with the column widths.
 */

export type TableView = "data" | "structure" | "definition"

export interface ViewState {
  view: TableView
  filters: Filter[]
  /** Every condition has to hold, or any one of them. */
  match: "all" | "any"
  sort: SortKey[]
  /** 1-based. */
  page: number
}

export const DEFAULT_VIEW: ViewState = {
  view: "data",
  filters: [],
  match: "all",
  sort: [],
  page: 1,
}

/** Every key of the address this page writes besides the selection. */
export const VIEW_KEYS = ["view", "filters", "match", "sort", "page"] as const

export const PAGE_SIZES = [50, 100, 300, 1000] as const
export const DEFAULT_PAGE_SIZE = 100

function decodeSort(raw: string): SortKey[] {
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.flatMap((entry) => {
      if (!entry || typeof entry !== "object") return []
      const { column, desc } = entry as Record<string, unknown>
      return typeof column === "string" && column !== "" ? [{ column, desc: desc === true }] : []
    })
  } catch {
    return []
  }
}

export function encodeSort(sort: readonly SortKey[]): string {
  if (sort.length === 0) return ""
  return JSON.stringify(sort.map(({ column, desc }) => ({ column, desc })))
}

/**
 * The view an address states. `param` is the context's reader.
 *
 * `where` is read as `filters` when the address has none of its own: it is
 * the name the search page hands a hit over under.
 */
export function readView(param: (name: string) => string): ViewState {
  const view = param("view")
  const page = Number(param("page"))
  return {
    view: view === "structure" || view === "definition" ? view : "data",
    filters: decodeFilters(param("filters") || param("where")),
    match: param("match") === "any" ? "any" : "all",
    sort: decodeSort(param("sort")),
    page: Number.isInteger(page) && page > 1 ? page : 1,
  }
}

/**
 * A change to the view as the keys of the address it writes: a value at its
 * default is cleared rather than spelled out, so the address of a table
 * nobody has arranged is just the table.
 */
export function viewParams(patch: Partial<ViewState>): Record<string, string | null> {
  const params: Record<string, string | null> = {}
  if (patch.view !== undefined) params.view = patch.view === "data" ? null : patch.view
  if (patch.filters !== undefined) {
    params.filters = encodeFilters(patch.filters) || null
    // The alias has been read; from here the address speaks in its own words.
    params.where = null
  }
  if (patch.match !== undefined) params.match = patch.match === "any" ? "any" : null
  if (patch.sort !== undefined) params.sort = encodeSort(patch.sort) || null
  if (patch.page !== undefined) params.page = patch.page > 1 ? String(patch.page) : null
  return params
}

/**
 * Whether two views show the same rows: the same conditions, order and page.
 * Which of the three views is open is not part of it — the structure of a
 * table can be read without its rows being read again.
 */
export function sameRows(a: ViewState, b: ViewState): boolean {
  return (
    a.page === b.page &&
    a.match === b.match &&
    encodeFilters(a.filters) === encodeFilters(b.filters) &&
    encodeSort(a.sort) === encodeSort(b.sort)
  )
}

/** What `GET /browse` is asked for a view. */
export function browseQuery(
  schema: string,
  table: string,
  view: Pick<ViewState, "filters" | "match" | "sort" | "page">,
  limit: number,
): Record<string, string | number | undefined> {
  return {
    schema,
    table,
    limit,
    offset: (view.page - 1) * limit,
    filters: encodeFilters(view.filters) || undefined,
    match: view.match === "any" ? "any" : undefined,
    sort: encodeSort(view.sort) || undefined,
  }
}

/** What `GET /count` and `GET /export` are asked for the same rows. */
export function rowsQuery(
  schema: string,
  table: string,
  view: Pick<ViewState, "filters" | "match">,
): Record<string, string | undefined> {
  return {
    schema,
    table,
    filters: encodeFilters(view.filters) || undefined,
    match: view.match === "any" ? "any" : undefined,
  }
}

/** What identifies the rows a count was taken of: the table and the conditions on it. */
export function countKey(
  schema: string,
  table: string,
  view: Pick<ViewState, "filters" | "match">,
) {
  return JSON.stringify([schema, table, encodeFilters(view.filters), view.match])
}

/**
 * The sort the grid's headers show. The server appends the primary key to
 * whatever was asked so two pages never share a row; that tie-break is the
 * server's business, and a header marked "sorted" for it would be a sort the
 * reader never chose and cannot clear.
 */
export function gridSort(sort: readonly SortKey[]): GridSort {
  return sort.map(({ column, desc }) => ({ column, desc }))
}

/** A Go duration ("1.234ms", "850µs", "2.1s") as the foot prints it. */
export function readableDuration(duration: string): string {
  const match = /^([\d.]+)(ns|µs|us|ms|s|m|h)$/.exec(duration)
  if (!match) return duration
  const value = Number(match[1])
  const unit = match[2] === "us" ? "µs" : match[2]
  if (!Number.isFinite(value)) return duration
  const rounded =
    value >= 100
      ? Math.round(value)
      : value >= 10
        ? Math.round(value * 10) / 10
        : Math.round(value * 100) / 100
  return `${rounded} ${unit}`
}

export interface PageFacts {
  /** 1-based row numbers of this page; 0 when it holds nothing. */
  from: number
  to: number
  /** Whether a page follows, as the server said (it read one row more). */
  hasNext: boolean
  hasPrevious: boolean
  /** The reader is on a page past the last row. */
  pastEnd: boolean
  /** The last page, when a total is known. */
  lastPage: number | null
}

/**
 * Where a page sits in its table.
 *
 * "Next" follows the server's word that another page exists, not a guess from
 * how full this one is: a table of exactly one page used to offer a second,
 * and the second said "this table is empty".
 */
export function pageFacts(
  page: Pick<BrowsePage, "rowCount" | "truncated" | "offset" | "limit">,
  total: number | null,
): PageFacts {
  const from = page.rowCount === 0 ? 0 : page.offset + 1
  const to = page.offset + page.rowCount
  return {
    from,
    to,
    hasNext: page.truncated,
    hasPrevious: page.offset > 0,
    pastEnd: page.rowCount === 0 && page.offset > 0,
    lastPage: total === null ? null : Math.max(1, Math.ceil(total / Math.max(page.limit, 1))),
  }
}

/** A count as the foot and the rail print it: digits grouped. */
export function grouped(n: number): string {
  return n.toLocaleString("en-US")
}

/** A row estimate at the rail's width: 1,204 → "1.2k", 60,000 → "60k". */
export function compactCount(n: number): string {
  if (n < 1000) return String(n)
  const units: [number, string][] = [
    [1e9, "B"],
    [1e6, "M"],
    [1e3, "k"],
  ]
  for (const [size, suffix] of units) {
    if (n < size) continue
    const value = n / size
    const text = value >= 100 ? String(Math.round(value)) : (Math.round(value * 10) / 10).toString()
    return `${text}${suffix}`
  }
  return String(n)
}
