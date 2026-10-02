import { ApiError, refusedIndex } from "@/lib/api"
import {
  isDefault,
  isNewRowId,
  rowKey,
  type CellValue,
  type ChangeCounts,
  type ChangeRef,
  type ChangeSet,
  type ChangesPayload,
  type ClippedCell,
  type EditValue,
  type GridColumn,
  type GridRow,
  type RowOrigin,
} from "@/components/database/grid"
import type { Filter } from "@/components/database/data/types"

/**
 * One row as the table editor's own parts read it — the inspector beside the
 * grid, the request that applies a set, the sentence that reports a refusal —
 * with no React in it.
 *
 * The grid keeps its own model of rows over a change set and does not hand it
 * out; what is here reads the same three things (the page as the server sent
 * it, the cells it cut, the staged set) by the same rules, for the places
 * outside the grid that show a row.
 */

/**
 * How a row is told from the others on its page: by its primary key, or —
 * `undefined` — by where it stands. A table with no key, and an engine whose
 * key repeats, leave it out so the grid knows its ids are positions.
 */
export function rowIdentity(
  columns: readonly GridColumn[],
  keyed: boolean,
): ((row: GridRow) => string) | undefined {
  if (!keyed) return undefined
  const keys = columns.flatMap((column, index) => (column.primaryKey ? [index] : []))
  if (keys.length === 0) return undefined
  return (row) => keys.map((index) => String(row[index])).join("\u0000")
}

/** A row as it was read, by column key, with the columns whose value was cut. */
export function rowOrigin(
  columns: readonly GridColumn[],
  row: GridRow,
  index: number,
  clipped: readonly ClippedCell[] | undefined,
): RowOrigin {
  const values: Record<string, CellValue> = {}
  columns.forEach((column, at) => {
    values[column.key] = row[at] ?? null
  })
  const cut = (clipped ?? []).flatMap((cell) =>
    cell.row === index && columns[cell.column] ? [columns[cell.column].key] : [],
  )
  return { values, clipped: cut }
}

const BIT_STRING = /^(?:bit|varbit|bit varying)\b/i

/**
 * The columns of a page that hold strings of bits written as text.
 *
 * The server calls every bit-string column `binary`, and an engine that sends
 * one as bytes (`\x0f`) is edited as bytes. PostgreSQL sends and takes
 * `00001111`: a bytes editor there wrote `\x00001111`, which the engine
 * refuses. Which of the two a column is, its values say — so a column typed as
 * a bit string is text where the page holds one written in ones and zeros.
 */
export function bitTextColumns(
  page:
    { types: readonly string[]; kinds?: readonly string[]; rows: readonly GridRow[] } | undefined,
): number[] {
  if (!page?.kinds) return []
  return page.kinds.flatMap((kind, index) => {
    if (kind !== "binary" || !BIT_STRING.test(page.types[index] ?? "")) return []
    const held = page.rows.map((row) => row[index]).filter((value) => value !== null)
    const written = held.length > 0 && held.every((v) => typeof v === "string" && /^[01]*$/.test(v))
    return written ? [index] : []
  })
}

export type RowState = "clean" | "inserted" | "updated" | "deleted"

export interface InspectedCell {
  column: GridColumn
  /** What the row holds now: the staged value where there is one. `undefined` = left to its default. */
  value: EditValue | undefined
  /** What the server sent. `undefined` for a row that is only staged. */
  original: CellValue | undefined
  /** A staged value stands over what was read. */
  edited: boolean
  /** Only the start of the value was loaded; `size` is the whole of it in bytes. */
  preview: boolean
  size?: number
}

export interface InspectedRow {
  id: string
  /** Where it is drawn: the page's rows in order, then the staged new ones. */
  index: number
  state: RowState
  /** The row as read, which a first edit needs. Absent for a staged new row. */
  origin?: RowOrigin
  cells: InspectedCell[]
}

export interface RowSource {
  columns: readonly GridColumn[]
  rows: readonly GridRow[]
  clipped?: readonly ClippedCell[]
  changes: ChangeSet
  rowId?: (row: GridRow) => string
}

/** How many rows the grid draws for a page: what was read, then what is staged to be added. */
export function drawnRows(source: Pick<RowSource, "rows" | "changes">): number {
  return source.rows.length + source.changes.inserts.length
}

/** The row drawn at `index`, cell by cell, or null when nothing is drawn there. */
export function inspectRow(source: RowSource, index: number): InspectedRow | null {
  const { columns, rows, clipped, changes, rowId } = source
  if (index < 0) return null
  if (index >= rows.length) {
    const inserted = changes.inserts[index - rows.length]
    if (!inserted) return null
    return {
      id: inserted.id,
      index,
      state: "inserted",
      cells: columns.map((column) => ({
        column,
        value: inserted.values[column.key],
        original: undefined,
        edited: column.key in inserted.values,
        preview: false,
      })),
    }
  }
  const row = rows[index]
  const id = rowId ? rowId(row) : String(index)
  const origin = rowOrigin(columns, row, index, clipped)
  const sizes = new Map<number, number>()
  for (const cell of clipped ?? []) if (cell.row === index) sizes.set(cell.column, cell.size)
  const update = changes.updates[id]
  const deleted = changes.deletes[id] !== undefined
  return {
    id,
    index,
    state: deleted ? "deleted" : update ? "updated" : "clean",
    origin,
    cells: columns.map((column, at) => {
      const staged = update !== undefined && column.key in update.values
      const value = staged ? update.values[column.key] : (row[at] ?? null)
      const cut = sizes.has(at) || (typeof row[at] === "string" && isPreviewText(row[at]))
      return {
        column,
        value,
        original: row[at] ?? null,
        edited: staged,
        // A value read whole and staged over the preview is no longer one.
        preview: cut && !staged,
        size: sizes.get(at),
      }
    }),
  }
}

/** The base API's own mark of a cut binary value: `\x…… (N bytes)`. */
function isPreviewText(value: string): boolean {
  return value.startsWith("\\x") && /… \(\d+ bytes\)$/.test(value)
}

/** A row as plain values by column name: unset and DEFAULT are left out. */
export function rowRecord(row: InspectedRow): Record<string, CellValue> {
  const record: Record<string, CellValue> = {}
  for (const cell of row.cells) {
    if (cell.value === undefined || isDefault(cell.value)) continue
    record[cell.column.name] = cell.value
  }
  return record
}

/**
 * The key `GET /cell` finds a row by: its primary key, or for a table without
 * one the row as read. The key travels in the address of the request, so a
 * keyless row is found by its short values only — a long text is what the
 * reader is trying to load, not what tells the row apart.
 */
export function cellKey(
  columns: readonly GridColumn[],
  origin: RowOrigin,
): Record<string, CellValue> {
  const key = rowKey(columns, origin.values, origin.clipped)
  if (columns.some((column) => column.primaryKey)) return key
  return Object.fromEntries(
    Object.entries(key).filter(([, value]) => typeof value !== "string" || value.length <= 200),
  )
}

/* ---------------------------------------------------------------- the set */

/** "3 changes — 2 edited, 1 new": the change bar's sentence. */
export function changeSummary(counts: ChangeCounts): string {
  const parts: string[] = []
  if (counts.updates > 0) parts.push(`${counts.updates} edited`)
  if (counts.inserts > 0) parts.push(`${counts.inserts} new`)
  if (counts.deletes > 0) parts.push(`${counts.deletes} deleted`)
  const head = counts.total === 1 ? "1 change" : `${counts.total} changes`
  return `${head} — ${parts.join(", ")}`
}

/**
 * Whether a column's read value can stand in a WHERE as a test that nobody
 * changed it meanwhile. A single-precision float cannot: MySQL compares the
 * column with the decimal text as doubles, the stored value is not that
 * double, and the edit would be refused as a conflict that never happened.
 */
export function guardable(typeName: string): boolean {
  return !/^float(?:\s*\(\s*\d+(?:\s*,\s*\d+)?\s*\))?(?:\s+unsigned)?$/i.test(typeName.trim())
}

/**
 * The payload with the unreliable guards taken out of each update's key. The
 * primary key stays whatever it is: without it the row is not found at all.
 */
export function relaxGuards(
  payload: ChangesPayload,
  columns: readonly GridColumn[],
): ChangesPayload {
  const loose = new Set(
    columns
      .filter((column) => !column.primaryKey && !guardable(column.typeName))
      .map((c) => c.name),
  )
  if (loose.size === 0 || !columns.some((column) => column.primaryKey)) return payload
  return {
    ...payload,
    changes: payload.changes.map((change) => {
      if (change.op !== "update") return change
      const key = Object.fromEntries(
        Object.entries(change.key).filter(([name]) => !loose.has(name)),
      )
      return { ...change, key }
    }),
  }
}

export interface Refusal {
  /** The staged row the server refused, when it named one. */
  rowId?: string
  /** Its place in the request: 1-based, of `total`. */
  position?: number
  total: number
  op?: ChangeRef["op"]
  /** How many rows the change matched, for a conflict. */
  matched?: number
  conflict: boolean
  /** The whole sentence for the reader. */
  message: string
}

const OP_WORD: Record<ChangeRef["op"], string> = {
  insert: "new row",
  update: "edit",
  delete: "delete",
}

/**
 * Why a set was not applied, as the server said it, and which staged row it
 * was about. The set is all or nothing, so every refusal ends the same way:
 * nothing was written.
 */
export function refusal(error: unknown, refs: readonly ChangeRef[]): Refusal {
  const total = refs.length
  const base = error instanceof Error ? error.message : String(error)
  if (!(error instanceof ApiError)) return { total, conflict: false, message: base }
  const index = refusedIndex(error.field, "changes")
  const ref = index === undefined ? undefined : refs.find((entry) => entry.index === index)
  const matched = /^matched (\d+) rows$/.exec(error.reason ?? "")
  const conflict = error.code === "change_conflict"
  let message = base
  if (conflict && ref) {
    const count = matched ? Number(matched[1]) : undefined
    const why =
      count === 0
        ? "the row was changed or deleted after it was read"
        : count === undefined
          ? base
          : `it matches ${count} rows, and a change has to match exactly one`
    message =
      `The ${OP_WORD[ref.op]} ${isNewRowId(ref.rowId) ? "" : "of this row "}was refused: ${why}`.replace(
        /\s+/g,
        " ",
      )
  }
  return {
    rowId: ref?.rowId,
    position: ref ? ref.index + 1 : undefined,
    total,
    op: ref?.op,
    matched: matched ? Number(matched[1]) : undefined,
    conflict,
    message,
  }
}

/* ------------------------------------------------- a foreign key's picker */

/** What the table a key points at is made of: its columns and what each holds. */
export interface PickerShape {
  columns: readonly string[]
  kinds: readonly string[]
}

/** How many of a table's text columns a typed word is looked for in. */
const SEARCHED = 6

const UUID_TEXT = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/**
 * How a referenced row is found by a typed word. `exact` is the key equal to
 * the word, where the word can be a key at all: a number typed for an integer
 * key is row 41 itself, and is listed first — not lost among every row with
 * 41 somewhere in its email. `inside` is the word within one of the table's
 * text columns, any of which may hold it.
 *
 * Both empty when the engine offers nothing to compare with; the caller then
 * has no search to make.
 */
export function pickerSearch(
  shape: PickerShape,
  key: string,
  word: string,
  operators: readonly string[],
): { exact: Filter | null; inside: Filter[] } {
  const kind = shape.kinds[shape.columns.indexOf(key)]
  const whole =
    kind === "integer"
      ? /^-?\d+$/.test(word)
      : kind === "decimal" || kind === "float"
        ? /^-?\d+(?:\.\d+)?$/.test(word)
        : kind === "uuid" && UUID_TEXT.test(word)
  const exact: Filter | null =
    whole && operators.includes("eq") ? { column: key, op: "eq", value: word } : null
  const op = operators.includes("icontains")
    ? "icontains"
    : operators.includes("contains")
      ? "contains"
      : null
  const inside: Filter[] = []
  if (op) {
    const textual = shape.columns.filter((_, index) => shape.kinds[index] === "text")
    for (const column of textual.slice(0, SEARCHED)) inside.push({ column, op, value: word })
  }
  return { exact, inside }
}

/** The kinds whose values say nothing in a line of text. */
const UNREADABLE = new Set(["binary", "json", "array"])

/**
 * What tells a referenced row from its neighbours beside its key: its first
 * few values that can be read at a glance, the text ones first.
 */
export function pickerLabel(
  columns: readonly string[],
  kinds: readonly string[],
  row: GridRow,
  keyAt: number,
): string {
  const readable = columns
    .map((_, index) => index)
    .filter((index) => index !== keyAt && !UNREADABLE.has(kinds[index] ?? ""))
    .filter((index) => {
      const value = row[index]
      return value !== null && value !== undefined && value !== "" && typeof value !== "object"
    })
  const text = readable.filter((index) => kinds[index] === "text")
  const rest = readable.filter((index) => kinds[index] !== "text")
  return [...text, ...rest]
    .slice(0, 3)
    .map((index) => {
      const value = String(row[index])
      return value.length > 40 ? `${value.slice(0, 40)}…` : value
    })
    .join(" · ")
}
