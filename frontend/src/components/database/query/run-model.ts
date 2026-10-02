import type { RunScope } from "@/components/database/query/sql-text"
import type {
  ExplainResponse,
  QueryResponse,
  QueryResult,
  Risk,
  ScriptResponse,
} from "@/components/database/query/types"

/**
 * What a run is, once it has been asked for: the statements it sent, what
 * became of each, and — while it is still out — when it left.
 *
 * One statement goes to `/query` and several to `/script`; the page reads
 * both as the same list of steps, so a single statement is a script of one
 * and nothing downstream has two cases.
 */

export interface RunStep {
  index: number
  sql: string
  /** 1-based line of the statement in the editor's text. */
  line: number
  risk?: Risk
  /** Skipped: it came after the statement that failed. */
  status: "ok" | "error" | "skipped"
  result?: QueryResult
  /** The engine's own words. */
  error?: string
  durationMs: number
  /** The rows this statement was asked again for, when that is more than the run's own limit. */
  limit?: number
}

export interface Run {
  /** The run itself, the same through a statement of it being asked again for more rows. */
  key?: string
  /** The name the server knows the request now out by, so it can be stopped. */
  queryId: string
  phase: "running" | "done"
  startedAt: number
  finishedAt?: number
  /** What was sent, and where it sat in the editor. */
  sql: string
  scope: RunScope
  offset: number
  line: number
  /** Rows asked for per statement. */
  limit: number
  /** Several statements, run on one session. */
  script: boolean
  steps: RunStep[]
  /** The step that stopped the run, or -1. */
  failed: number
  transaction?: ScriptResponse["transaction"]
  risk?: Risk
  /** Nothing was sent, and this is why. */
  refused?: string
  /** The request as a whole was turned down: the server's words. */
  error?: string
  /** The reader stopped it. */
  cancelled?: boolean
  cancelling?: boolean
  /**
   * One statement of the run is out again for more rows: its index, and how
   * many. The other statements keep what they came back with.
   */
  fetching?: { index: number; limit: number }
}

/** What tells one run from the next, whatever request of it is out. */
export const runKey = (run: Run) => run.key ?? run.queryId

/** The rows a statement was last asked for: its own when it was asked again, else the run's. */
export const stepLimit = (run: Run, step: RunStep) => step.limit ?? run.limit

export interface Explained {
  phase: "running" | "done"
  startedAt: number
  finishedAt?: number
  sql: string
  /** The statement was run to measure it. */
  analyze: boolean
  answer?: ExplainResponse
  refused?: string
  error?: string
}

/** What a tab holds beside its text: its last run and its last plan. */
export interface TabRun {
  run?: Run
  explain?: Explained
}

/** A Go duration ("1.2ms", "700µs", "1m2.5s") in milliseconds; 0 for what is not one. */
export function goDurationMs(text: string): number {
  const units: Record<string, number> = {
    ns: 1e-6,
    µs: 1e-3,
    us: 1e-3,
    ms: 1,
    s: 1000,
    m: 60_000,
    h: 3_600_000,
  }
  let total = 0
  let matched = false
  for (const part of text.matchAll(/([\d.]+)(ns|µs|us|ms|s|m|h)/g)) {
    const value = Number(part[1])
    if (!Number.isFinite(value)) return 0
    total += value * units[part[2]]
    matched = true
  }
  return matched ? total : 0
}

/** Milliseconds as a reader says them: "0.7 ms", "12 ms", "1.24 s", "2 min 5 s". */
export function spanText(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return ""
  if (ms < 1) return `${Math.round(ms * 100) / 100} ms`
  if (ms < 10) return `${Math.round(ms * 10) / 10} ms`
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 60_000) return `${Math.round(ms / 10) / 100} s`
  const minutes = Math.floor(ms / 60_000)
  const seconds = Math.round((ms % 60_000) / 1000)
  return seconds > 0 ? `${minutes} min ${seconds} s` : `${minutes} min`
}

/** The clock of a run still out: "0:07", "12:40". */
export function elapsedText(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  const minutes = Math.floor(seconds / 60)
  return `${minutes}:${String(seconds % 60).padStart(2, "0")}`
}

/** The one step of a `/query` answer. */
export function stepsOfQuery(answer: QueryResponse, sql: string, line: number): RunStep[] {
  return [
    {
      index: 0,
      sql,
      line,
      risk: answer.risk,
      status: "ok",
      result: answer.result,
      durationMs: goDurationMs(answer.result.duration),
    },
  ]
}

/** The steps of a `/script` answer, each on its line of the editor's text. */
export function stepsOfScript(answer: ScriptResponse, line: number): RunStep[] {
  return (answer.statements ?? []).map((step) => ({
    index: step.index,
    sql: step.sql,
    line: line + step.line - 1,
    risk: step.risk,
    status: step.status,
    result: step.result,
    error: step.error,
    // The server rounds a step to whole milliseconds; the result's own duration is finer.
    durationMs: step.result
      ? goDurationMs(step.result.duration) || step.durationMs
      : step.durationMs,
  }))
}

const count = (n: number, one: string, many = `${one}s`) =>
  `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`

/** What one step did, in a few words: "120 rows", "3 rows changed", "done". */
export function stepOutcome(step: RunStep): string {
  if (step.status === "skipped") return "not run"
  if (step.status === "error") return "failed"
  const result = step.result
  if (!result) return "done"
  if (result.columns.length > 0) {
    return `${count(result.rowCount, "row")}${result.truncated ? ", and more" : ""}`
  }
  return result.rowsAffected > 0 ? `${count(result.rowsAffected, "row")} changed` : "done"
}

export interface Message {
  key: string
  tone: "default" | "success" | "warning" | "danger"
  /** The statement the line is about, where it is about one. */
  step?: RunStep
  text: string
  detail?: string
}

const TRANSACTION: Record<NonNullable<Run["transaction"]>, string | null> = {
  none: null,
  read_only: "Run in one read-only scope: nothing could be written.",
  committed: "One transaction, committed: every statement took effect.",
  rolled_back: "One transaction, rolled back: nothing took effect.",
}

/** The run told in order: what each statement did, and how the whole ended. */
export function messagesOf(run: Run | undefined): Message[] {
  if (!run) return []
  const lines: Message[] = []
  if (run.refused) {
    lines.push({ key: "refused", tone: "warning", text: "Nothing was run.", detail: run.refused })
    return lines
  }
  for (const step of run.steps) {
    lines.push({
      key: `step-${step.index}`,
      step,
      tone: step.status === "error" ? "danger" : step.status === "skipped" ? "default" : "success",
      text: stepOutcome(step),
      detail: step.error,
    })
  }
  if (run.error)
    lines.push({
      key: "error",
      tone: "danger",
      text: "The run was turned down.",
      detail: run.error,
    })
  if (run.phase === "running") return lines
  if (run.cancelled) {
    lines.push({
      key: "cancelled",
      tone: "warning",
      text: "Stopped: the statement was cancelled on the server.",
    })
  }
  const outcome = run.transaction ? TRANSACTION[run.transaction] : null
  if (outcome) {
    lines.push({
      key: "transaction",
      tone: run.transaction === "rolled_back" ? "warning" : "default",
      text: outcome,
    })
  }
  if (run.steps.length > 1) {
    const ok = run.steps.filter((step) => step.status === "ok").length
    const skipped = run.steps.filter((step) => step.status === "skipped").length
    lines.push({
      key: "summary",
      tone: run.failed >= 0 ? "danger" : "default",
      text:
        run.failed >= 0
          ? `Stopped at statement ${run.failed + 1} of ${run.steps.length}: ${count(ok, "statement")} ran${skipped > 0 ? `, ${skipped} did not` : ""}.`
          : `${count(ok, "statement")} ran in ${spanText((run.finishedAt ?? run.startedAt) - run.startedAt)}.`,
    })
  }
  return lines
}

/** The step a reader should be looking at when a run lands: the one that failed, else the last that returned rows, else the last. */
export function stepToShow(run: Run): number {
  if (run.failed >= 0 && run.failed < run.steps.length) return run.failed
  for (let at = run.steps.length - 1; at >= 0; at--) {
    if ((run.steps[at].result?.columns.length ?? 0) > 0) return at
  }
  return Math.max(0, run.steps.length - 1)
}

/** Whether a statement that came back cut may be asked again for more: only one that reads. */
export function canFetchMore(step: RunStep, limit: number, max: number): boolean {
  return Boolean(step.result?.truncated) && step.risk?.level === "read" && limit < max
}

/**
 * A run with one of its statements answered again, for more rows: that
 * statement takes the new rows and every other keeps what it had — its
 * result, its failure, its place among the chips.
 */
export function withRefetched(
  run: Run,
  index: number,
  answer: QueryResponse,
  limit: number,
): RunStep[] {
  return run.steps.map((step) =>
    step.index === index
      ? {
          ...step,
          status: "ok",
          error: undefined,
          risk: answer.risk,
          result: answer.result,
          durationMs: goDurationMs(answer.result.duration),
          limit,
        }
      : step,
  )
}

const DEFINES = new Set(["CREATE", "ALTER", "DROP", "RENAME", "COMMENT", "ATTACH", "DETACH"])

/**
 * Whether a run that came back may have changed what the connection holds —
 * a table made, altered, dropped or renamed — so that the schema the tree
 * and the completion read is worth reading again.
 */
export function definesSchema(run: Run): boolean {
  if (run.refused || run.risk?.level === "read") return false
  const first = (sql: string) =>
    (/^(?:\s|--[^\n]*\n|\/\*[\s\S]*?\*\/)*([A-Za-z]+)/.exec(sql)?.[1] ?? "").toUpperCase()
  const statements = run.steps.length > 0 ? run.steps.map((step) => step.sql) : [run.sql]
  return statements.some((sql) => DEFINES.has(first(sql)))
}
