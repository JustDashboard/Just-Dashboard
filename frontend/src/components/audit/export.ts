import type { AuditEntry } from "@/lib/types"
import { csvCell } from "@/lib/metrics-export"

/** The most one request for the trail may ask for (`auditPageMax` on the server). */
export const EXPORT_PAGE = 500

/**
 * The most rows one export holds. A trail can be millions of rows, and a file
 * built in a browser tab has to end somewhere: past this the export says it
 * stopped, and a narrower filter is how the rest is reached.
 */
export const EXPORT_MOST = 20_000

const COLUMNS: [label: string, value: (entry: AuditEntry) => string | number | undefined][] = [
  ["id", (e) => e.id],
  ["time", (e) => e.ts],
  ["user", (e) => e.username],
  ["role", (e) => e.role],
  ["actor", (e) => e.actor],
  ["key", (e) => e.keyName],
  ["key id", (e) => e.keyId],
  ["hub user", (e) => e.hubUser],
  ["session", (e) => e.sessionId],
  ["address", (e) => e.ip],
  ["action", (e) => e.action],
  ["target", (e) => e.target],
  ["method", (e) => e.method],
  ["path", (e) => e.path],
  ["status", (e) => e.status || undefined],
  ["result", (e) => (e.success ? "ok" : "failed")],
  ["detail", (e) => e.detail],
]

/**
 * A spreadsheet opens a cell that starts with one of these as a formula, and
 * a target or a detail is whatever a request carried. A leading apostrophe is
 * the cell's own way of saying "this is text".
 */
function inert(value: string) {
  return /^[=+\-@\t\r]/.test(value) ? `'${value}` : value
}

/** The entries as a spreadsheet: one row each, every field the trail keeps. */
export function auditCsv(entries: AuditEntry[]): string {
  const header = COLUMNS.map(([label]) => label).join(",")
  const lines = entries.map((entry) =>
    COLUMNS.map(([, value]) => {
      const cell = value(entry)
      return csvCell(cell === undefined || cell === null ? "" : inert(String(cell)))
    }).join(","),
  )
  return [header, ...lines].join("\n") + "\n"
}

/** "audit-2026-11-13.csv", or "audit-docker-failed-2026-11-13.csv" for a narrowed trail. */
export function exportName(
  filters: { username: string; action: string; failed: boolean },
  today: Date,
) {
  const parts = [filters.username, filters.action, filters.failed && "failed"]
    .filter((part): part is string => Boolean(part))
    .map((part) => part.replace(/\W+/g, "-").replace(/^-+|-+$/g, ""))
    .filter(Boolean)
  return ["audit", ...parts, today.toISOString().slice(0, 10)].join("-") + ".csv"
}
