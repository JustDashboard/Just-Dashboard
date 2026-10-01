import { isDefault, type EditValue } from "./values"
import type { CellValue, GridBlockCell, GridColumn } from "./types"

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
 * A GitHub-flavoured Markdown table, for pasting rows into an issue or a note.
 * A pipe would end the cell and a line break would end the row, so the one is
 * escaped and the other becomes `<br>`.
 */
export function toMarkdown(
  header: readonly string[],
  matrix: readonly (readonly string[])[],
): string {
  const encode = (field: string) => field.replace(/\|/g, "\\|").replace(/\r?\n|\r/g, "<br>")
  const line = (row: readonly string[]) => `| ${row.map(encode).join(" | ")} |`
  return [line(header), line(header.map(() => "---")), ...matrix.map(line)].join("\n")
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

/** A copied block in the grid's own format. */
export interface GridClip {
  cells: CellValue[][]
  /**
   * The cells that are only the start of a value the server cut. They travel
   * marked, so that pasting one somewhere is refused instead of writing the
   * first few kilobytes of a value as if they were all of it.
   */
  previews: GridBlockCell[]
}

export function encodeGridClip(
  matrix: readonly (readonly CellValue[])[],
  previews: readonly GridBlockCell[] = [],
): string {
  return JSON.stringify({
    v: 1,
    cells: matrix,
    ...(previews.length > 0 && { previews: previews.map((cell) => [cell.row, cell.column]) }),
  })
}

export function decodeGridClip(text: string): GridClip | null {
  try {
    const parsed = JSON.parse(text) as { v?: unknown; cells?: unknown; previews?: unknown }
    if (parsed?.v !== 1 || !Array.isArray(parsed.cells)) return null
    if (!parsed.cells.every((row) => Array.isArray(row))) return null
    const marks = Array.isArray(parsed.previews) ? parsed.previews : []
    return {
      cells: parsed.cells as CellValue[][],
      previews: marks.flatMap((mark) =>
        Array.isArray(mark) && Number.isInteger(mark[0]) && Number.isInteger(mark[1])
          ? [{ row: mark[0] as number, column: mark[1] as number }]
          : [],
      ),
    }
  } catch {
    return null
  }
}

/**
 * What a copy adds to its announcement when some of what it took is only the
 * start of a value. On the clipboard a preview looks like any other text, so
 * this is the one moment it can be said.
 */
export function previewNote(count: number): string {
  if (count === 0) return ""
  return count === 1
    ? " — one value is only its start"
    : ` — ${count.toLocaleString("en-US")} values are only their start`
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
