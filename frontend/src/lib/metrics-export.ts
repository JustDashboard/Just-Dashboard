import type { ChartRowLike } from "@/components/metrics/metric-chart"

/**
 * The rows on screen as a spreadsheet.
 *
 * Every monitor with a following ends up needing this: a chart is the right
 * way to see a week and the wrong way to hand it to somebody — a hosting
 * provider disputing steal, a colleague with their own tooling, a ticket that
 * wants numbers rather than a screenshot. Grafana hides it under Inspect →
 * Data; here it is a button beside the range, and it exports exactly the
 * window and resolution the charts are drawn from, peaks included.
 */
export type CsvColumn = { key: string; label: string }

/** The columns are taken from the rows themselves, so a new series is exported the day it is drawn. */
export function csvColumns(rows: ChartRowLike[]): CsvColumn[] {
  const keys = new Set<string>()
  for (const row of rows) {
    for (const key of Object.keys(row)) {
      // `t` and `at` are the axis and tooltip labels, already covered by the
      // ISO timestamp; nothing else on a row is a string worth a column.
      if (key === "ts" || key === "t" || key === "at") continue
      keys.add(key)
    }
  }
  return [...keys].map((key) => ({ key, label: key }))
}

export function rowsToCsv(rows: ChartRowLike[], columns: CsvColumn[] = csvColumns(rows)): string {
  const header = ["time", ...columns.map((c) => c.label)].map(csvCell).join(",")
  const lines = rows.map((row) => {
    const cells = [new Date(row.ts).toISOString(), ...columns.map((c) => cellValue(row[c.key]))]
    return cells.map(csvCell).join(",")
  })
  return [header, ...lines].join("\n") + "\n"
}

function cellValue(value: unknown): string {
  // A gap in the record is an empty cell, not a zero: a zero would turn an
  // outage into an hour of idle load in whatever reads the file next.
  if (value === null || value === undefined) return ""
  if (typeof value === "number") return Number.isFinite(value) ? String(value) : ""
  return String(value)
}

function csvCell(value: string): string {
  return /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value
}

/** Hands the browser a file to save. */
export function downloadText(filename: string, text: string, type = "text/csv") {
  const url = URL.createObjectURL(new Blob([text], { type }))
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  document.body.append(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}
