import { isDefault, type EditValue } from "./values"
import type { CellValue, GridColumn } from "./types"

/**
 * A block of cells as text, and back.
 *
 * Tab-separated is what a spreadsheet puts on the clipboard and what it takes
 * from it, so that is what Ctrl+C writes and what Ctrl+V reads. The format has
 * no standard, only a convention every spreadsheet follows: a field holding a
 * tab, a line break or a quote is wrapped in quotes with its own quotes
 * doubled. A serialiser that skips that turns one multi-line cell into three
 * rows on the way out, and a parser that skips it does the same on the way in.
 */

const NEEDS_QUOTES_TSV = /[\t\n\r"]/
const NEEDS_QUOTES_CSV = /[,\n\r"]/

function quote(field: string): string {
  return `"${field.replace(/"/g, '""')}"`
}

/** A matrix of fields as tab-separated text. Rows end in `\n`, the last one included. */
export function toTSV(matrix: readonly (readonly string[])[]): string {
  return matrix
    .map((row) =>
      row.map((field) => (NEEDS_QUOTES_TSV.test(field) ? quote(field) : field)).join("\t"),
    )
    .join("\n")
}

export interface CSVOptions {
  /** Column names for a first line. */
  header?: readonly string[]
  /**
   * Put an apostrophe in front of a field a spreadsheet would run as a formula
   * (one that begins `=`, `+`, `-` or `@`). Off by default: it changes the data,
   * which is wrong for a file another program will read, and right for one a
   * person is about to open in a spreadsheet.
   */
  guardFormulas?: boolean
}

/** RFC 4180: comma-separated, CRLF line ends, quotes doubled. */
export function toCSV(matrix: readonly (readonly string[])[], options: CSVOptions = {}): string {
  const encode = (field: string) => {
    const guarded = options.guardFormulas && /^[=+\-@\t\r]/.test(field) ? `'${field}` : field
    return NEEDS_QUOTES_CSV.test(guarded) ? quote(guarded) : guarded
  }
  const lines = matrix.map((row) => row.map(encode).join(","))
  if (options.header) lines.unshift(options.header.map(encode).join(","))
  return lines.join("\r\n")
}

/**
 * Reads delimited text into rows of fields.
 *
 * Handles what a spreadsheet actually writes: quoted fields holding the
 * delimiter, line breaks and doubled quotes; `\r\n`, `\n` and bare `\r` line
 * ends; and the one trailing line end every spreadsheet adds, which is not an
 * extra empty row. A quote only opens a quoted field at the start of one — a
 * measurement like `5" disk` in the middle of a field is text.
 */
export function parseDelimited(text: string, delimiter: string): string[][] {
  const rows: string[][] = []
  let row: string[] = []
  let field = ""
  let quoted = false
  // Whether the field under the cursor has had anything in it yet, and whether
  // the row has: `""` alone is one row holding one empty field, not nothing.
  let fieldStart = true
  let rowOpen = false
  let i = 0

  const endField = () => {
    row.push(field)
    field = ""
    fieldStart = true
  }
  const endRow = () => {
    endField()
    rows.push(row)
    row = []
    rowOpen = false
  }

  while (i < text.length) {
    const ch = text[i]
    if (quoted) {
      if (ch === '"') {
        if (text[i + 1] === '"') {
          field += '"'
          i += 2
          continue
        }
        quoted = false
        i++
        continue
      }
      field += ch
      i++
      continue
    }
    if (ch === "\r" || ch === "\n") {
      endRow()
      i += ch === "\r" && text[i + 1] === "\n" ? 2 : 1
      continue
    }
    rowOpen = true
    if (ch === '"' && fieldStart) {
      quoted = true
      fieldStart = false
    } else if (ch === delimiter) {
      endField()
    } else {
      field += ch
      fieldStart = false
    }
    i++
  }
  // Whatever follows the last line end is a row only if there is something in it.
  if (rowOpen) endRow()
  return rows
}

export function parseTSV(text: string): string[][] {
  return parseDelimited(text, "\t")
}

/**
 * A value as clipboard text. NULL is the empty field, which is what a
 * spreadsheet shows for one; the exact matrix travels beside the text (see
 * `GRID_MIME`) so a copy pasted back into a grid keeps NULL apart from "".
 */
export function clipText(value: EditValue | undefined): string {
  if (value === null || value === undefined || isDefault(value)) return ""
  if (typeof value === "object") return JSON.stringify(value)
  return String(value)
}

/**
 * The clipboard format a grid writes beside the plain text and prefers when it
 * reads: the cells as JSON, so NULL, the empty string, a number that is a
 * string and a boolean all come back as what they were.
 */
export const GRID_MIME = "application/x-jd-grid+json"

export function encodeGridClip(matrix: readonly (readonly CellValue[])[]): string {
  return JSON.stringify({ v: 1, cells: matrix })
}

export function decodeGridClip(text: string): CellValue[][] | null {
  try {
    const parsed = JSON.parse(text) as { v?: unknown; cells?: unknown }
    if (parsed?.v !== 1 || !Array.isArray(parsed.cells)) return null
    if (!parsed.cells.every((row) => Array.isArray(row))) return null
    return parsed.cells as CellValue[][]
  } catch {
    return null
  }
}

/**
 * Names for the keys of a JSON row. Two columns called `id` become `id` and
 * `id_2`: an object cannot hold both under one key, and dropping one silently
 * is how a join's result lost a column on the way to the clipboard.
 */
export function uniqueNames(columns: readonly Pick<GridColumn, "name">[]): string[] {
  const taken = new Set<string>()
  return columns.map(({ name }) => {
    let candidate = name
    for (let n = 2; taken.has(candidate); n++) candidate = `${name}_${n}`
    taken.add(candidate)
    return candidate
  })
}

/** Rows as an array of objects, pretty-printed. Values are the wire values, untouched. */
export function toJSONRows(
  columns: readonly Pick<GridColumn, "name">[],
  rows: readonly (readonly CellValue[])[],
): string {
  const names = uniqueNames(columns)
  return JSON.stringify(
    rows.map((row) => Object.fromEntries(names.map((name, i) => [name, row[i] ?? null]))),
    null,
    2,
  )
}
