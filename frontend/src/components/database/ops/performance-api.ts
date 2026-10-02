import { post, type Query } from "@/lib/api"
import { read, record } from "@/components/database/home/read"
import type {
  ChMerge,
  ChMutation,
  ChParts,
  ChQuery,
  DbActivityResponse,
  DbExplainResponse,
  DbIndexStats,
  DbLocks,
  DbMaintenanceList,
  DbMaintenanceRequest,
  DbMaintenanceResult,
  DbReplication,
  DbStatementSort,
  DbStatements,
  DbTableStats,
  SqliteFile,
} from "@/components/database/ops/performance-types"

/**
 * The operations routes of one SQL server, each read in the shape its
 * contract states.
 *
 * Every read is checked before a view is handed it (`read`): a view maps the
 * list it was promised, and an answer in another shape — a server a release
 * behind — is that view's "could not be read" rather than a thrown render.
 */

const base = (id: number) => `/databases/${id}`

/**
 * A read whose failure says how long it had been waiting. A read queued
 * behind a lock does not fail: it is given up, half a minute later, by
 * whatever stands between the page and the server — and the page says that
 * differently from a refusal (`ViewRead`).
 */
export async function timed<T>(work: Promise<T>): Promise<T> {
  const since = Date.now()
  try {
    return await work
  } catch (error) {
    if (error instanceof Error) Object.assign(error, { waitedMs: Date.now() - since })
    throw error
  }
}

export const readActivity = (id: number, signal?: AbortSignal) =>
  read<DbActivityResponse>(
    `${base(id)}/activity`,
    (answer) => Array.isArray(answer.sessions),
    undefined,
    signal,
  )

export const cancelStatement = (id: number, pid: string) =>
  post<{ cancelled: string }>(`${base(id)}/activity/cancel`, { pid })

export const endSession = (id: number, pid: string) =>
  post<{ killed: string }>(`${base(id)}/activity/kill`, { pid })

export const readLocks = (id: number, signal?: AbortSignal) =>
  read<DbLocks>(`${base(id)}/locks`, (answer) => Array.isArray(answer.waits), undefined, signal)

export const readReplication = (id: number, signal?: AbortSignal) =>
  timed(
    read<DbReplication>(
      `${base(id)}/replication`,
      (answer) => Array.isArray(answer.replicas),
      undefined,
      signal,
    ),
  )

/** How many tables or indexes one read asks for; the server's cap is 1000. */
export const STORAGE_LIMIT = 500

export const readTableStats = (id: number, signal?: AbortSignal) =>
  timed(
    read<DbTableStats>(
      `${base(id)}/tablestats`,
      (answer) => Array.isArray(answer.tables),
      { limit: STORAGE_LIMIT },
      signal,
    ),
  )

export const readIndexStats = (id: number, signal?: AbortSignal) =>
  timed(
    read<DbIndexStats>(
      `${base(id)}/indexstats`,
      (answer) => Array.isArray(answer.indexes),
      { limit: STORAGE_LIMIT },
      signal,
    ),
  )

export const readMaintenance = (id: number, signal?: AbortSignal) =>
  read<DbMaintenanceList>(
    `${base(id)}/maintenance`,
    (answer) => Array.isArray(answer.actions),
    undefined,
    signal,
  )

/**
 * Held open until the command finishes, up to half an hour. Aborting the
 * signal stops it on the server. The answer is checked like a read's: the run
 * dialog prints its statements and its output, and one in another shape is a
 * failure said in the dialog rather than a render that throws.
 */
export async function runMaintenance(
  id: number,
  request: DbMaintenanceRequest,
  signal?: AbortSignal,
): Promise<DbMaintenanceResult> {
  const result = await post<DbMaintenanceResult>(`${base(id)}/maintenance`, request, { signal })
  if (!record(result) || !Array.isArray(result.statements) || !Array.isArray(result.output)) {
    throw new Error("The server answered in a form this page does not read.")
  }
  return result
}

export const readStatements = (
  id: number,
  query: { sort: DbStatementSort; limit: number },
  signal?: AbortSignal,
) =>
  read<DbStatements>(
    `${base(id)}/statements`,
    (answer) => Array.isArray(answer.statements),
    query satisfies Query,
    signal,
  )

export const resetStatements = (id: number) =>
  post<{ ok: boolean; statement: string }>(`${base(id)}/statements/reset`, {})

export const enableExtension = (id: number, name: string) =>
  post<unknown>(`${base(id)}/server/extensions`, { name })

/** The plan of a statement, as the engine prints it. Nothing is executed. */
export const explainStatement = (id: number, query: string, signal?: AbortSignal) =>
  post<DbExplainResponse>(`${base(id)}/explain`, { query, format: "text" }, { signal })

export const readChParts = (id: number, query: { table?: string }, signal?: AbortSignal) =>
  read<ChParts>(
    `${base(id)}/clickhouse/parts`,
    (answer) => Array.isArray(answer.partitions),
    query,
    signal,
  )

export const readChMerges = (id: number, signal?: AbortSignal) =>
  read<{ merges: ChMerge[] }>(
    `${base(id)}/clickhouse/merges`,
    (answer) => Array.isArray(answer.merges),
    undefined,
    signal,
  )

export const readChMutations = (id: number, signal?: AbortSignal) =>
  read<{ mutations: ChMutation[] }>(
    `${base(id)}/clickhouse/mutations`,
    (answer) => Array.isArray(answer.mutations),
    undefined,
    signal,
  )

export const readChQueries = (id: number, signal?: AbortSignal) =>
  read<{ queries: ChQuery[] }>(
    `${base(id)}/clickhouse/queries`,
    (answer) => Array.isArray(answer.queries),
    undefined,
    signal,
  )

export const readSqliteFile = (id: number, signal?: AbortSignal) =>
  read<SqliteFile>(`${base(id)}/sqlite/file`, (answer) => record(answer.objects), undefined, signal)

/** A statement the page asked the server to classify, as the Query page would before running it. */
export type DbRisk = { destructive: boolean; level: string; reasons?: string[] }

export const classifyStatement = (id: number, query: string, signal?: AbortSignal) =>
  post<DbRisk>(`${base(id)}/classify`, { query }, { signal })

/** One statement of a script, as the server ran it. */
export type DbScriptStep = {
  index: number
  sql: string
  status: "ok" | "error" | "skipped"
  error?: string
}

export type DbScriptResult = {
  statements: DbScriptStep[]
  /** The index of the statement that stopped the script, or -1. */
  failed: number
}

/**
 * Runs statements in order on one session, stopping at the first that fails.
 * The server decides what the role may run by the worst statement among them,
 * exactly as it does for the Query page.
 */
export async function runScript(id: number, script: string): Promise<DbScriptResult> {
  const result = await post<DbScriptResult>(`${base(id)}/script`, { script })
  if (!record(result) || !Array.isArray(result.statements)) {
    throw new Error("The server answered in a form this page does not read.")
  }
  return result
}
