import type { CellValue } from "@/components/database/grid"
import { planText } from "@/components/database/query/plan"
import type { ExplainResponse, QueryResult } from "@/components/database/query/types"

/**
 * The assistant's side of the SQL editor, as data: what was asked, what the
 * answer said as it streamed in, and where each answer ended.
 *
 * The conversation itself — what the provider is sent — is the server's, so
 * this is only the page's reading of it: the server's id for it once a turn
 * of it has been kept, and the turns as they were drawn. Kept free of React,
 * so `bun test` reads it without a DOM.
 */

export type AssistantTaskKind = "sql" | "error" | "plan" | "policy"

/** A result attached to a question: the only way a row is ever sent. */
export interface AttachedResult {
  columns: string[]
  rows: CellValue[][]
  /** How many rows the run returned, of which `rows` are the first. */
  total: number
}

/** One question, as `POST /databases/{id}/assistant` takes it. */
export interface AssistantTask {
  kind: AssistantTaskKind
  prompt: string
  statement?: string
  error?: string
  plan?: string
  table?: string
  result?: AttachedResult
}

export interface ToolCall {
  id: string
  tables: string[]
}

/** What the conversation was told about the schema, as the server counted it. */
export interface ContextStats {
  schema: string
  described: number
  named: number
  omitted: number
}

export interface Usage {
  input: number
  output: number
  cacheRead?: number
  cacheWrite?: number
}

export type Stop = "end" | "tool" | "length" | "refusal"

/** One line of a streamed answer. */
export type AssistantEvent =
  | {
      type: "start"
      conversation: string
      provider: string
      model: string
      modelLabel: string
      context: ContextStats
    }
  | { type: "thinking" }
  | { type: "text"; text: string }
  | { type: "described"; calls: { tables: string[] | null; missing: string[] | null }[] }
  | {
      type: "done"
      stop: Stop
      usage: Usage
      model: string
      kept: boolean
      pending?: ToolCall[]
    }
  | { type: "error"; error: { code: string; message: string } }

/** A piece of an answer, in the order it arrived. */
export type Part =
  | { kind: "text"; text: string }
  /** The assistant asked to read tables; `answer` once the person said. */
  | { kind: "ask"; calls: ToolCall[]; answer?: "allowed" | "declined" }
  /** What was read for it. */
  | { kind: "read"; tables: string[]; missing: string[] }

export type Phase =
  "waiting" | "thinking" | "writing" | "asking" | "done" | "stopped" | "failed" | "refused" | "cut"

/** What a question carried, as words for the page: never the values. */
export interface Asked {
  kind: AssistantTaskKind
  prompt: string
  table?: string
  attached: string[]
}

export interface Turn {
  id: string
  asked: Asked
  parts: Part[]
  phase: Phase
  error?: string
  model?: string
  usage?: Usage
}

export interface Transcript {
  /** The server's id for the conversation, once a turn of it has been kept. */
  conversation: string
  /** The id the last answer began under, kept only if its turn is. */
  candidate: string
  /** The schema the conversation is about: another one begins another. */
  schema: string
  context?: ContextStats
  modelLabel?: string
  provider?: string
  turns: Turn[]
}

export const EMPTY_TRANSCRIPT: Transcript = {
  conversation: "",
  candidate: "",
  schema: "",
  turns: [],
}

/** How many rows and columns of a result are ever attached, as the server bounds them. */
export const ATTACH_ROWS = 50
export const ATTACH_COLUMNS = 30

/** A result as it is attached: its first rows, its first columns, and how many there were. */
export function attachable(result: QueryResult): AttachedResult {
  return {
    columns: result.columns.slice(0, ATTACH_COLUMNS),
    rows: result.rows.slice(0, ATTACH_ROWS).map((row) => row.slice(0, ATTACH_COLUMNS)),
    total: result.rowCount,
  }
}

/**
 * A plan as the assistant is given it: the engine's own JSON, the plan it
 * printed, or — where it answered with a table of steps — that table, a
 * line a step.
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

const plural = (n: number, one: string, many: string) =>
  `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`

/** The words a question's attachments are listed by. */
export function askedOf(task: AssistantTask): Asked {
  const attached: string[] = []
  if (task.statement) attached.push("statement")
  if (task.error) attached.push("error")
  if (task.plan) attached.push("plan")
  if (task.result) attached.push(`result · ${plural(task.result.rows.length, "row", "rows")}`)
  return { kind: task.kind, prompt: task.prompt, table: task.table, attached }
}

/**
 * Splits what has arrived into whole lines, keeping the unfinished last one
 * for the next chunk: a line of JSON is often cut across two reads.
 */
export function readLines(held: string, chunk: string): { lines: string[]; rest: string } {
  const text = held + chunk
  const parts = text.split("\n")
  const rest = parts.pop() ?? ""
  return { lines: parts.filter((line) => line.trim() !== ""), rest }
}

/** One line as an event, or null for a line this page does not know. */
export function parseEvent(line: string): AssistantEvent | null {
  try {
    const event = JSON.parse(line) as AssistantEvent
    return event && typeof event === "object" && typeof event.type === "string" ? event : null
  } catch {
    return null
  }
}

function withTurn(t: Transcript, id: string, change: (turn: Turn) => Turn): Transcript {
  return { ...t, turns: t.turns.map((turn) => (turn.id === id ? change(turn) : turn)) }
}

/** A new turn, waiting for its first line. */
export function beginTurn(
  t: Transcript,
  id: string,
  task: AssistantTask,
  schema: string,
): Transcript {
  // A question about another schema is a conversation of its own: the one
  // that was running was told about the other one.
  const fresh = t.schema !== schema || t.conversation === ""
  const base: Transcript = fresh
    ? { ...EMPTY_TRANSCRIPT, schema, turns: t.schema === schema ? t.turns : [] }
    : t
  return {
    ...base,
    candidate: "",
    turns: [...base.turns, { id, asked: askedOf(task), parts: [], phase: "waiting" }],
  }
}

/** Folds one line of the answer into the turn it belongs to. */
export function applyEvent(t: Transcript, id: string, event: AssistantEvent): Transcript {
  switch (event.type) {
    case "start":
      return withTurn(
        {
          ...t,
          candidate: event.conversation,
          context: event.context ?? t.context,
          modelLabel: event.modelLabel,
          provider: event.provider,
        },
        id,
        (turn) => ({ ...turn, model: event.modelLabel }),
      )
    case "thinking":
      return withTurn(t, id, (turn) =>
        turn.parts.at(-1)?.kind === "text" ? turn : { ...turn, phase: "thinking" },
      )
    case "text":
      return withTurn(t, id, (turn) => {
        const last = turn.parts.at(-1)
        const parts =
          last?.kind === "text"
            ? [...turn.parts.slice(0, -1), { kind: "text" as const, text: last.text + event.text }]
            : [...turn.parts, { kind: "text" as const, text: event.text }]
        return { ...turn, parts, phase: "writing" }
      })
    case "described":
      return withTurn(t, id, (turn) => ({
        ...turn,
        parts: [
          ...turn.parts,
          ...event.calls.map((call) => ({
            kind: "read" as const,
            tables: call.tables ?? [],
            missing: call.missing ?? [],
          })),
        ],
      }))
    case "done": {
      const phase: Phase =
        event.stop === "tool"
          ? "asking"
          : event.stop === "length"
            ? "cut"
            : event.stop === "refusal"
              ? "refused"
              : "done"
      const kept = event.kept ? { conversation: t.candidate || t.conversation } : {}
      return withTurn({ ...t, ...kept }, id, (turn) => ({
        ...turn,
        phase,
        usage: event.usage,
        parts:
          event.pending && event.pending.length > 0
            ? [...turn.parts, { kind: "ask" as const, calls: event.pending }]
            : turn.parts,
      }))
    }
    case "error":
      return endTurn(t, id, "failed", event.error.message)
  }
}

/** A turn that ended without its answer: stopped by the person, or failed. */
export function endTurn(
  t: Transcript,
  id: string,
  phase: "stopped" | "failed",
  error?: string,
): Transcript {
  return withTurn(t, id, (turn) => ({ ...turn, phase, error }))
}

/** The calls the last answer is waiting on, if it is waiting. */
export function pendingCalls(t: Transcript): ToolCall[] {
  const last = t.turns.at(-1)
  if (!last || last.phase !== "asking") return []
  const ask = last.parts.findLast((part) => part.kind === "ask")
  return ask?.kind === "ask" && !ask.answer ? ask.calls : []
}

/** The person's answer to the calls, written into the turn that asked. */
export function decide(t: Transcript, allow: boolean): Transcript {
  const last = t.turns.at(-1)
  if (!last) return t
  return withTurn(t, last.id, (turn) => {
    const at = turn.parts.findLastIndex((part) => part.kind === "ask")
    if (at < 0) return turn
    const parts = [...turn.parts]
    parts[at] = {
      ...(parts[at] as Extract<Part, { kind: "ask" }>),
      answer: allow ? "allowed" : "declined",
    }
    return { ...turn, parts, phase: "waiting" }
  })
}

/** Whether a turn is still coming in. */
export const answering = (turn: Turn | undefined) =>
  turn !== undefined && ["waiting", "thinking", "writing"].includes(turn.phase)

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

/** The title a statement opened from an answer takes in its tab. */
export function statementTitle(asked: Asked): string {
  if (asked.kind === "policy") return `Policy on ${asked.table ?? "table"}`
  const words = asked.prompt.trim().replace(/\s+/g, " ")
  if (!words) return asked.kind === "error" ? "Corrected statement" : "From the assistant"
  return words.length > 40 ? `${words.slice(0, 39)}…` : words
}
