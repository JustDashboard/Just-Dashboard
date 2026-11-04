import type { CellValue } from "@/components/database/grid"
import { planText } from "@/components/database/query/plan"
import type { ExplainResponse, QueryResult } from "@/components/database/query/types"
import type { AgentAttachable } from "@/components/assistant/agent/chat"
import { ATTACHED_TEXT } from "@/components/assistant/agent/model"

/**
 * What the SQL editor's tab lets a question carry to the assistant, as text the
 * model reads: the statement, the last result as a small table, the statement the
 * last run failed on with the engine's words, the last plan. Each goes only when
 * the person presses it, and the chat draws only its label. Kept free of React so
 * `bun test` reads it without a DOM.
 */

/** What the editor's tab holds that a question can carry. */
export interface QueryContext {
  /** The statement in the editor: the one the cursor is on, or all of it. */
  statement: string
  /** The tab's last result. */
  result?: QueryResult
  /** The statement the tab's last run failed on, and the engine's words. */
  failed?: { sql: string; error: string }
  /** The tab's last plan, as the assistant reads it (`planSource`). */
  plan?: { sql: string; text: string }
}

/** How much of a result goes: its first rows and columns, each value cut short. */
export const TABLE_ROWS = 50
export const TABLE_COLUMNS = 30
export const TABLE_CELL = 80

const plural = (n: number, one: string, many: string) =>
  `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`

/** A value as one line of a table: NULL said, a document as JSON, a long one cut. */
function cellText(value: CellValue): string {
  const text =
    value === null ? "NULL" : typeof value === "object" ? JSON.stringify(value) : String(value)
  const line = text.replace(/\s*[\r\n]+\s*/g, " ")
  const points = [...line]
  return points.length > TABLE_CELL ? `${points.slice(0, TABLE_CELL - 1).join("")}…` : line
}

const width = (text: string) => [...text].length
const pad = (text: string, to: number) => text + " ".repeat(Math.max(0, to - width(text)))

/**
 * A result as a small table of text, the way `psql` prints one: its first rows
 * and columns, each value on one line, and how many there were in all, so the
 * model knows what it was not given. Rows stop where the text would pass what a
 * question may carry.
 */
export function resultTable(result: Pick<QueryResult, "columns" | "rows" | "rowCount">): {
  text: string
  rows: number
} {
  const columns = result.columns.slice(0, TABLE_COLUMNS)
  const cells = result.rows
    .slice(0, TABLE_ROWS)
    .map((row) => columns.map((_, at) => cellText(row[at] ?? null)))
  const widths = columns.map((name, at) =>
    Math.max(width(name), ...cells.map((row) => width(row[at]))),
  )
  const line = (values: string[]) =>
    values
      .map((value, at) => pad(value, widths[at]))
      .join(" | ")
      .trimEnd()
  const head = [line(columns), widths.map((n) => "-".repeat(n)).join("-+-")]
  const body: string[] = []
  // Room is kept for the line that says how many there were.
  let size = head.join("\n").length + 80
  for (const row of cells) {
    const printed = line(row)
    if (size + printed.length + 1 > ATTACHED_TEXT) break
    body.push(printed)
    size += printed.length + 1
  }
  const total = Math.max(result.rowCount, result.rows.length)
  const left = result.columns.length - columns.length
  const foot = [
    total > body.length
      ? `(${plural(body.length, "row", "rows")} of ${total.toLocaleString("en-US")})`
      : `(${plural(total, "row", "rows")})`,
    left > 0 ? `${plural(left, "more column", "more columns")} left out` : "",
  ]
    .filter(Boolean)
    .join(", ")
  return { text: [...head, ...body, foot].join("\n"), rows: body.length }
}

/**
 * A plan as the assistant is given it: the engine's own JSON, the plan it
 * printed, or — where it answered with a table of steps — that table, a line a
 * step.
 */
export function planSource(answer: ExplainResponse): string {
  if (answer.plan !== undefined) return JSON.stringify(answer.plan, null, 2)
  const printed = planText(answer.result)
  if (printed !== null) return printed
  const cell = (value: CellValue) =>
    value === null ? "" : typeof value === "object" ? JSON.stringify(value) : String(value)
  return [
    answer.result.columns.join("\t"),
    ...answer.result.rows.map((row) => row.map(cell).join("\t")),
  ].join("\n")
}

/** The statement that failed and what the engine said, as one attachment's text. */
export const errorText = (failed: { sql: string; error: string }) =>
  `The statement that failed:\n${failed.sql.trim()}\n\nWhat the engine said:\n${failed.error.trim()}`

/** A statement and its plan, as one attachment's text. */
export const planAttachmentText = (plan: { sql: string; text: string }) =>
  `The statement:\n${plan.sql.trim()}\n\nIts plan:\n${plan.text}`

/** What the tab offers to attach right now, in the order the toggles stand. */
export function queryAttachables(context: QueryContext): AgentAttachable[] {
  const out: AgentAttachable[] = []
  const statement = context.statement.trim()
  if (statement) {
    out.push({
      id: "statement",
      label: "statement",
      text: statement,
      description: "Attach the statement in the editor",
    })
  }
  if (context.result && context.result.columns.length > 0) {
    const table = resultTable(context.result)
    out.push({
      id: "result",
      label: `result · ${plural(table.rows, "row", "rows")}`,
      text: table.text,
      rows: table.rows,
      description: `Attach the first ${plural(table.rows, "row", "rows")} of the result`,
    })
  }
  if (context.failed) {
    out.push({
      id: "error",
      label: "error",
      text: errorText(context.failed),
      description: "Attach the statement that failed and its error",
    })
  }
  if (context.plan) {
    out.push({
      id: "plan",
      label: "plan",
      text: planAttachmentText(context.plan),
      description: "Attach the last plan",
    })
  }
  return out
}

/** The question the results' Ask about this error asks. */
export const ERROR_QUESTION = "Why did this statement fail, and how do I fix it?"
/** The question the plan's Ask about this plan asks. */
export const PLAN_QUESTION = "Explain this plan: what does it do, and what would make it faster?"

/** The fence languages an answer's statements are written under. */
const SQL_FENCES = new Set([
  "",
  "sql",
  "pgsql",
  "postgresql",
  "postgres",
  "plpgsql",
  "mysql",
  "mariadb",
  "sqlite",
  "tsql",
  "mssql",
  "plsql",
  "oracle",
  "clickhouse",
])

/** A fenced block that is a statement to open, rather than an example in another language. */
export const isStatementFence = (lang: string) => SQL_FENCES.has(lang.toLowerCase())

/** The title a statement opened from an answer takes in its tab: the question it answered. */
export function statementTitle(question: string): string {
  const words = question.trim().replace(/\s+/g, " ")
  if (!words) return "From the assistant"
  return words.length > 40 ? `${words.slice(0, 39)}…` : words
}
