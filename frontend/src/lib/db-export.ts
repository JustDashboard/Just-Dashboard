import type { QueryResult } from "@/lib/types"

export function resultToCSV(result: Pick<QueryResult, "columns" | "rows">): string {
  const escape = (value: unknown) => {
    if (value === null || value === undefined) return ""
    const text = typeof value === "object" ? JSON.stringify(value) : String(value)
    return /[",\r\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text
  }
  return [
    result.columns.map(escape).join(","),
    ...result.rows.map((row) => row.map(escape).join(",")),
  ].join("\n")
}
