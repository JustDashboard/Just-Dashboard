import type { DbExportFormat } from "@/lib/types"
import type { DbExportState } from "@/components/database/data/types"
import { grouped } from "@/components/database/data/view"

/**
 * How an export ended, read from the signs the server leaves.
 *
 * A file that downloads says nothing about whether it is the whole table. The
 * server therefore says so three ways: a trailer a browser cannot read, a
 * status it keeps for a quarter of an hour under an id the client chose, and
 * — in the formats with somewhere to put it — a closing mark inside the file
 * itself. The status is asked first; the mark is the answer when the status
 * is gone (the server restarted, or forgot). CSV and TSV have no mark, so
 * without a status the honest answer for them is "not known".
 */

/** The rows one export takes unless asked for more, and the most it can be asked for. */
export const EXPORT_ROWS = 100_000
export const EXPORT_ROWS_MAX = 1_000_000

export const EXPORT_FORMATS: Record<DbExportFormat, { label: string; detail: string }> = {
  csv: { label: "CSV", detail: "commas, a header line" },
  tsv: { label: "TSV", detail: "tabs, for a spreadsheet" },
  json: { label: "JSON", detail: "one array of objects" },
  ndjson: { label: "JSON Lines", detail: "one object a line" },
  sql: { label: "SQL", detail: "INSERT statements" },
}

export interface ExportEnd {
  status: "complete" | "truncated" | "failed" | "unknown"
  /** Rows written, where a sign said. */
  rows?: number
  error?: string
}

/** How much of the end of a file is read to find its mark. */
export const MARK_TAIL = 2048

/**
 * The closing mark of an exported file, from its last bytes as text.
 *
 * SQL always ends with a comment saying how it went. JSON and JSON Lines end
 * with one extra `__export` entry only when they are *not* complete, so a
 * tail with no such entry is a complete file. CSV and TSV carry nothing.
 */
export function exportMark(format: DbExportFormat, tail: string): ExportEnd | null {
  if (format === "sql") {
    const lines = tail.trimEnd().split("\n")
    const last = lines[lines.length - 1] ?? ""
    const complete = /^-- export complete: (\d+) rows?/.exec(last)
    if (complete) return { status: "complete", rows: Number(complete[1]) }
    const truncated = /^-- export truncated at (\d+) rows?/.exec(last)
    if (truncated) return { status: "truncated", rows: Number(truncated[1]) }
    const failed = /^-- export failed after (\d+) rows?: ?(.*)$/.exec(last)
    if (failed) return { status: "failed", rows: Number(failed[1]), error: failed[2] }
    // A SQL export with no closing comment was cut off before it could write one.
    return { status: "failed", error: "The file ends without its closing line" }
  }
  if (format === "json" || format === "ndjson") {
    const at = tail.lastIndexOf('{"__export":')
    if (at < 0) return { status: "complete" }
    const text = tail.slice(at).replace(/[\]\s]+$/, "")
    try {
      const mark = (JSON.parse(text) as { __export?: Record<string, unknown> }).__export
      if (mark && (mark.status === "truncated" || mark.status === "failed")) {
        return {
          status: mark.status,
          rows: typeof mark.rows === "number" ? mark.rows : undefined,
          error: typeof mark.error === "string" ? mark.error : undefined,
        }
      }
    } catch {
      // A mark that does not parse is a file that was cut inside it.
    }
    return { status: "failed", error: "The file ends inside its closing mark" }
  }
  return null
}

/** The server's own record of an export as its ending; null while it is still running. */
export function exportStateEnd(state: DbExportState): ExportEnd | null {
  if (state.status === "running") return null
  return { status: state.status, rows: state.rows, error: state.error }
}

/**
 * The ending to report: the server's record where there is one, else the mark
 * in the file, else — a CSV whose status is gone — that it is not known.
 */
export function exportEnd(
  state: DbExportState | null,
  format: DbExportFormat,
  tail: string,
): ExportEnd {
  return (state && exportStateEnd(state)) ?? exportMark(format, tail) ?? { status: "unknown" }
}

/** What the reader is told about an ended export. */
export function exportReport(
  end: ExportEnd,
  table: string,
  limit: number,
): { tone: "success" | "warning" | "danger"; title: string; description?: string } {
  const rows =
    end.rows === undefined ? undefined : `${grouped(end.rows)} ${end.rows === 1 ? "row" : "rows"}`
  switch (end.status) {
    case "complete":
      return {
        tone: "success",
        title: rows ? `Exported ${rows} from ${table}` : `Exported ${table}`,
      }
    case "truncated":
      return {
        tone: "warning",
        title: `The export of ${table} is not the whole table`,
        description: `It stopped at the ${grouped(limit)}-row limit${rows ? ` with ${rows} written` : ""}. The table holds more.`,
      }
    case "failed":
      return {
        tone: "danger",
        title: `The export of ${table} failed`,
        description: `${end.error ?? "The server ended it early"}${rows ? ` — after ${rows}` : ""}. No file was saved.`,
      }
    default:
      return {
        tone: "warning",
        title: `Exported ${table}`,
        description:
          "The server no longer says how the export ended, and this format cannot say it in the file. Check the last rows.",
      }
  }
}
