import type { GridColumnKind } from "@/components/database/grid"
import { OPERATORS, filterValues } from "@/components/database/data/filters"
import type { Filter } from "@/components/database/data/types"

/**
 * The statement behind a view, with its values written in: what "Open as
 * query" hands to the editor.
 *
 * The server says which statement it ran for a page — in the engine's own
 * dialect, with a bind marker where each value went. Only it knows how this
 * engine quotes a name, pages a result or looks inside a text, so the text is
 * the server's and nothing here builds SQL. What is done here is the last
 * step a person would do by hand to run it elsewhere: each marker replaced by
 * the value that was bound to it, as a literal.
 *
 * The values are known because the page sent them — the filters in order, then
 * the page size and the offset — and each marker says by where it stands what
 * it holds: one followed by `ESCAPE '!'` is a LIKE pattern, one after LIMIT or
 * FETCH NEXT is the page size, one after OFFSET is the offset. When the
 * markers and the values do not add up, nothing is guessed: the answer is
 * null and the caller hands the statement over as it ran.
 */

type Marker = { start: number; end: number; index: number }

const QUOTES: Record<string, string> = { '"': '"', "`": "`", "[": "]", "'": "'" }

/** Every bind marker outside a quoted name or a literal, in the order they stand. */
function markers(sql: string): Marker[] {
  const found: Marker[] = []
  let sequence = 0
  let i = 0
  while (i < sql.length) {
    const char = sql[i]
    const close = QUOTES[char]
    if (close) {
      i++
      while (i < sql.length) {
        if (sql[i] === close) {
          // A doubled closing quote is that character inside the name.
          if (sql[i + 1] === close) i++
          else break
        }
        i++
      }
      i++
      continue
    }
    if (char === "?") {
      found.push({ start: i, end: i + 1, index: sequence++ })
      i++
      continue
    }
    const numbered = /^(?:\$|@p|:)(\d+)/.exec(sql.slice(i, i + 12))
    // `::text` is a cast, and `a:1` inside a name was skipped above.
    if (numbered && sql[i - 1] !== ":") {
      found.push({ start: i, end: i + numbered[0].length, index: Number(numbered[1]) - 1 })
      i += numbered[0].length
      continue
    }
    i++
  }
  return found
}

/** A value as a literal of the statement's own dialect. */
function literal(value: string, bare: boolean, sql: string): string {
  if (bare && /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/.test(value)) return value
  let text = value.replace(/'/g, "''")
  // The engines that quote a name with a backtick read a backslash in a
  // literal as an escape; the others take it as written.
  if (sql.includes("`")) text = text.replace(/\\/g, "\\\\")
  // SQL Server reads a plain literal in the database's code page.
  const national = sql.includes("[") && /[^\x00-\x7f]/.test(value)
  return `${national ? "N" : ""}'${text}'`
}

/** The text of a LIKE pattern for a value, escaped the way the server escapes it. */
function likePattern(value: string, op: Filter["op"], sql: string): string {
  const wildcards = sql.includes("[") ? "!%_[" : "!%_"
  let escaped = ""
  for (const char of value) escaped += wildcards.includes(char) ? `!${char}` : char
  if (op === "prefix") return `${escaped}%`
  if (op === "suffix") return `%${escaped}`
  return `%${escaped}%`
}

const SUBSTRING = new Set(["contains", "not_contains", "icontains", "prefix", "suffix"])

/**
 * `statement` with every marker replaced by its value, or null when the
 * markers are not the ones this view would have bound.
 *
 * `kinds` says which columns hold numbers, so `id > 5` is written with a bare
 * 5; everything else is a quoted literal, which every engine here reads into
 * the column's own type.
 */
export function inlineStatement(
  statement: string,
  filters: readonly Filter[],
  page: { limit: number; offset: number },
  kinds: Readonly<Record<string, GridColumnKind>> = {},
): string | null {
  const found = markers(statement)
  const binds: { filter: Filter; value: string }[] = []
  for (const filter of filters) {
    if (OPERATORS[filter.op].arity === "none") continue
    for (const value of filterValues(filter)) binds.push({ filter, value })
  }
  if (found.length !== binds.length + 2) return null

  let out = ""
  let at = 0
  for (const marker of found) {
    const before = statement.slice(0, marker.start)
    const after = statement.slice(marker.end)
    let text: string
    if (marker.index < binds.length) {
      const { filter, value } = binds[marker.index]
      const pattern = SUBSTRING.has(filter.op) && /^\)? ESCAPE '!'/.test(after)
      text = pattern
        ? literal(likePattern(value, filter.op, statement), false, statement)
        : literal(value, kinds[filter.column] === "number" && !SUBSTRING.has(filter.op), statement)
    } else if (/(?:LIMIT|FETCH NEXT|FETCH FIRST)\s*$/i.test(before)) {
      text = String(page.limit)
    } else if (/OFFSET\s*$/i.test(before)) {
      text = String(page.offset)
    } else {
      return null
    }
    out += statement.slice(at, marker.start) + text
    at = marker.end
  }
  return out + statement.slice(at)
}
