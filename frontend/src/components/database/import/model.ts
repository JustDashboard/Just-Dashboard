import { bytes, plural } from "@/lib/format"
import type { DbDriver } from "@/lib/types"
import type { DotTone } from "@/components/status-dot"
import {
  EMPTY_CONNECT_FIELDS,
  composeDsn,
  pastedDsn,
  type ConnectFields,
} from "@/components/database/connect/dsn"
import type {
  DbCopyProgress,
  DbImport,
  DbImportLive,
  DbImportMode,
  DbImportStatus,
  DbSlotLag,
  DbSubscriptionState,
} from "@/components/database/import/types"

/**
 * What the import page says about an import, kept out of the components so it
 * can be tested with no server: the spine's steps, how far along a copy is,
 * and the words for a table's state and for how far behind the target is.
 */

/** What a publication, a subscription and a slot may be called. */
export const IMPORT_NAME = /^[a-z_][a-z0-9_]{0,49}$/

export function importNameProblem(name: string): string | undefined {
  if (IMPORT_NAME.test(name.trim())) return undefined
  return "Lowercase letters, digits and underscores, starting with a letter or an underscore, at most 50."
}

/** The steps of the sequence an import is, from the state the server keeps. */
export function importSteps(mode: DbImportMode): { key: string; label: string }[] {
  const steps = [
    { key: "source", label: "Source" },
    { key: "plan", label: "Plan" },
    { key: "copy", label: "Copy" },
  ]
  return mode === "replication" ? [...steps, { key: "cutover", label: "Cutover" }] : steps
}

/**
 * Which step of the spine an import is on. Nothing chosen yet is the source
 * (0) or, once the source has answered, the plan (1); an import under way is
 * the copy, and for a replication whose copy has finished it is the cutover.
 * One that has ended is past the last step.
 */
export function importStep(
  mode: DbImportMode,
  state: { inspected: boolean; import?: DbImport; caughtUp?: boolean },
): number {
  const steps = importSteps(mode).length
  const imp = state.import
  if (!imp) return state.inspected ? 1 : 0
  if (imp.status === "done") return steps
  if (imp.status === "failed" || imp.status === "cancelled") return 2
  if (imp.mode === "replication" && (imp.status === "cutting" || state.caughtUp)) return 3
  return 2
}

/** The word for where an import is, and the dot it wears. */
export function statusWords(status: DbImportStatus): { word: string; tone: DotTone } {
  switch (status) {
    case "running":
      return { word: "running", tone: "notice" }
    case "replicating":
      return { word: "replicating", tone: "running" }
    case "cutting":
      return { word: "cutting over", tone: "notice" }
    case "done":
      return { word: "done", tone: "running" }
    case "failed":
      return { word: "failed", tone: "danger" }
    case "cancelled":
      return { word: "stopped", tone: "stopped" }
  }
}

/** An import is still holding something on a server. */
export function isLive(status: DbImportStatus): boolean {
  return status === "running" || status === "replicating" || status === "cutting"
}

/** Where from, and where to, as a line. */
export function importTitle(imp: Pick<DbImport, "source" | "database">): string {
  const from = imp.source.database
    ? `${imp.source.database} on ${imp.source.address}`
    : imp.source.address
  return `${from} → ${imp.database}`
}

/** What the target's database could be called, from the source's. */
export function suggestedDatabase(source: string, taken: readonly string[]): string {
  const base = source.replace(/[^A-Za-z0-9_]+/g, "_").replace(/^_+|_+$/g, "") || "imported"
  const start = /^[A-Za-z_]/.test(base) ? base : `db_${base}`
  const used = new Set(taken.map((name) => name.toLowerCase()))
  if (!used.has(start.toLowerCase())) return start.slice(0, 63)
  for (let n = 2; ; n += 1) {
    const candidate = `${start.slice(0, 55)}_${n}`
    if (!used.has(candidate.toLowerCase())) return candidate
  }
}

const TABLE_WORDS: Record<DbSubscriptionState, string> = {
  waiting: "waiting",
  copying: "copying",
  copied: "copied",
  synchronizing: "catching up",
  ready: "ready",
}

export function tableStateWords(state: DbSubscriptionState): string {
  return TABLE_WORDS[state]
}

export function tableStateTone(state: DbSubscriptionState): DotTone {
  if (state === "ready") return "running"
  if (state === "waiting") return "stopped"
  return "notice"
}

/**
 * How far behind the source the target is: nothing, a figure, or why there is
 * none to give.
 */
export function lagWords(lag: DbSlotLag | undefined): string {
  if (!lag) return "—"
  if (!lag.exists) return "no slot"
  if (lag.bytes <= 0) return "caught up"
  return `${bytes(lag.bytes)} behind`
}

/** The copy as a share of what was expected, 0–100, or `undefined` when nothing says. */
export function copyShare(
  live: Pick<DbImportLive, "import" | "progress" | "subscription">,
): number | undefined {
  const { subscription, progress } = live
  if (live.import.mode === "replication") {
    if (!subscription || subscription.tables.length === 0) return undefined
    return Math.round((subscription.ready / subscription.tables.length) * 100)
  }
  if (!progress) return undefined
  if (progress.estimatedRows > 0) {
    // The planner's count is an estimate and the table can hold more: the bar
    // stops short of full until the job says it is over.
    return Math.min(99, Math.round((progress.rows / progress.estimatedRows) * 100))
  }
  if (progress.tables > 0)
    return Math.min(99, Math.round((progress.tablesDone / progress.tables) * 100))
  return undefined
}

/** What a streamed copy has done so far, in a line. */
export function progressWords(progress: DbCopyProgress | undefined): string {
  if (!progress) return "Starting"
  // An engine whose dump is a file is read whole before it is loaded.
  if (progress.phase === "dumping") return "Reading the source into a dump"
  if (progress.phase === "loading") return `Loading the dump · ${bytes(progress.bytes)}`
  const parts = []
  if (progress.tables > 0)
    parts.push(`${progress.tablesDone} of ${plural(progress.tables, "table")}`)
  if (progress.rows > 0) parts.push(plural(progress.rows, "row"))
  if (progress.bytes > 0) parts.push(`${bytes(progress.bytes)} read`)
  return parts.length > 0 ? parts.join(" · ") : "Starting"
}

/** Every table has been copied and the target is applying changes. */
export function copyFinished(live: Pick<DbImportLive, "subscription">): boolean {
  const state = live.subscription
  return Boolean(state?.exists && state.tables.length > 0 && state.ready === state.tables.length)
}

/**
 * Whether the cutover can be asked for: a replication that is replicating and
 * has copied every table. The server checks the same again.
 */
export function canCutOver(live: Pick<DbImportLive, "import" | "subscription">): boolean {
  return (
    live.import.mode === "replication" && live.import.status === "replicating" && copyFinished(live)
  )
}

/** A table of the subscription as the page draws it: its name, its state, what it has taken. */
export function tableLine(table: { schema: string; name: string }): string {
  return table.schema === "public" ? table.name : `${table.schema}.${table.name}`
}

/** How much a source holds, from the tables a replication report lists. */
export function sourceTotals(tables: { estimatedRows: number; size: number }[]): {
  tables: number
  rows: number
  size: number
  uncounted: boolean
} {
  let rows = 0
  let size = 0
  let uncounted = false
  for (const table of tables) {
    if (table.estimatedRows >= 0) rows += table.estimatedRows
    else uncounted = true
    size += table.size
  }
  return { tables: tables.length, rows, size, uncounted }
}

/** What is typed for the source: its address as fields, or as a string pasted whole. */
export type SourceDraft = {
  shape: "fields" | "url"
  fields: ConnectFields
  pasted: string
}

export const EMPTY_SOURCE: SourceDraft = {
  shape: "fields",
  fields: EMPTY_CONNECT_FIELDS,
  pasted: "",
}

/** The connection string the draft says, or nothing while it names no host. */
export function sourceDsn(driver: DbDriver, draft: SourceDraft): string {
  if (draft.shape === "url") return pastedDsn(driver, draft.pasted)
  return draft.fields.host.trim() ? composeDsn(driver, draft.fields) : ""
}
