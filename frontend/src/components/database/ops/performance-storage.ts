import type { Capability } from "@/lib/types"
import type {
  DbCheckpointMode,
  DbIndexStat,
  DbMaintenanceAction,
  DbMaintenanceRequest,
  DbTableStat,
} from "@/components/database/ops/performance-types"

/**
 * What the Tables and Indexes views decide before they draw: which columns
 * an engine can fill, which table has earned a vacuum, how the rows are
 * ordered, and which of the engine's maintenance actions apply to a table, to
 * an index or to the whole database — for this role, on this connection.
 *
 * The server sends `-1` for a count the engine does not keep. It is never
 * drawn as a zero here: a column no row can fill is left out, and a cell one
 * row cannot fill is a dash.
 */

/** A table's place in the address: its schema and its name, each escaped, so a dot in either is not a separator. */
export function objectParam(schema: string, name: string): string {
  return `${encodeURIComponent(schema)}/${encodeURIComponent(name)}`
}

export function parseObject(param: string): { schema: string; name: string } | null {
  const at = param.indexOf("/")
  if (at < 0) return null
  try {
    return {
      schema: decodeURIComponent(param.slice(0, at)),
      name: decodeURIComponent(param.slice(at + 1)),
    }
  } catch {
    return null
  }
}

/** A figure the engine keeps, or nothing: `-1` is the server's "not counted". */
export function counted(value: number | undefined): number | undefined {
  return value === undefined || value < 0 ? undefined : value
}

export type TableColumn =
  "engine" | "dead" | "bloat" | "scans" | "vacuum" | "analyze" | "parts" | "compression"

/** The columns some row can fill. The name, the rows and the size are every engine's. */
export function tableColumns(tables: readonly DbTableStat[]): Set<TableColumn> {
  const columns = new Set<TableColumn>()
  for (const table of tables) {
    if (table.engine) columns.add("engine")
    if (table.deadRows >= 0) columns.add("dead")
    if (table.bloatBytes >= 0) columns.add("bloat")
    if (table.seqScans >= 0 || table.indexScans >= 0) columns.add("scans")
    if (table.lastVacuum || table.lastAutovacuum) columns.add("vacuum")
    if (table.lastAnalyze || table.lastAutoanalyze) columns.add("analyze")
    if (table.parts !== undefined) columns.add("parts")
    if (table.uncompressedBytes !== undefined && table.uncompressedBytes > 0) {
      columns.add("compression")
    }
  }
  return columns
}

/**
 * What each column of the table takes, in pixels. The table is laid out
 * fixed — the name has what the others leave — so whether it fits is
 * arithmetic, and it is done on the width of the view, not the window: the
 * rail beside the page takes its share of every window.
 */
const TABLE_WIDTHS = {
  name: 190,
  rows: 64,
  size: 152,
  actions: 48,
  dead: 100,
  bloat: 104,
  scans: 132,
  parts: 64,
  compression: 100,
  vacuum: 128,
  analyze: 128,
} as const

/** The columns given up for room, first to last: what qualifies a figure goes before the figure. */
const DROPPED_FIRST = ["scans", "bloat", "compression", "analyze", "vacuum"] as const

/** Under this the table is not a table: its rows are drawn down, and nothing is dropped. */
export const TABLE_FROM = 560

/**
 * The columns a table of this width draws, of the ones the engine fills, or
 * `null` where there is no room for a table at all. Columns go one at a time
 * until the rest fit; a width that would take more than that is the other
 * layout, with every figure in it.
 */
export function fittedColumns(
  columns: ReadonlySet<TableColumn>,
  width: number,
): Set<TableColumn> | null {
  if (width < TABLE_FROM) return null
  const kept = new Set(columns)
  const needed = () =>
    TABLE_WIDTHS.name +
    TABLE_WIDTHS.rows +
    TABLE_WIDTHS.size +
    TABLE_WIDTHS.actions +
    [...kept].reduce((sum, column) => sum + (column === "engine" ? 0 : TABLE_WIDTHS[column]), 0)
  for (const column of DROPPED_FIRST) {
    if (needed() <= width) break
    kept.delete(column)
  }
  return kept
}

/** A column's width in the fixed table, as a style. The name's is left to the table. */
export function columnWidth(column: Exclude<keyof typeof TABLE_WIDTHS, "name">): { width: number } {
  return { width: TABLE_WIDTHS[column] }
}

/** The newer of a manual run and the engine's own, and which it was. */
export function lastRun(
  manual: string | undefined,
  automatic: string | undefined,
): { at: string; automatic: boolean } | undefined {
  const a = manual ? Date.parse(manual) : Number.NaN
  const b = automatic ? Date.parse(automatic) : Number.NaN
  if (Number.isNaN(a) && Number.isNaN(b)) return undefined
  if (Number.isNaN(b) || a >= b) return { at: manual as string, automatic: false }
  return { at: automatic as string, automatic: true }
}

/** Dead rows as a share of every row version the table holds, 0–1. */
export function deadShare(table: DbTableStat): number | undefined {
  const dead = counted(table.deadRows)
  if (dead === undefined) return undefined
  const live = Math.max(table.rows, 0)
  return dead + live > 0 ? dead / (dead + live) : 0
}

/**
 * A table a vacuum is overdue on: more than ten thousand dead rows, and more
 * than a fifth of the live ones — the advisor's own line, so the two pages
 * call out the same tables.
 */
export function needsVacuum(table: DbTableStat): boolean {
  const dead = counted(table.deadRows)
  return dead !== undefined && dead > 10_000 && dead > Math.max(table.rows, 0) * 0.2
}

/** A table the planner has no statistics for: it has rows and has never been analysed. */
export function neverAnalysed(table: DbTableStat, columns: ReadonlySet<TableColumn>): boolean {
  return (
    columns.has("analyze") &&
    !table.lastAnalyze &&
    !table.lastAutoanalyze &&
    (table.rows > 0 || table.inserts > 0)
  )
}

/** Sequential scans as a share of every scan of the table, 0–1; nothing where it was never scanned. */
export function seqShare(table: DbTableStat): number | undefined {
  const seq = counted(table.seqScans)
  const index = counted(table.indexScans)
  if (seq === undefined || index === undefined || seq + index === 0) return undefined
  return seq / (seq + index)
}

/** How much smaller the table is on disk than its data: ClickHouse's compression, as a ratio. */
export function compressionRatio(table: DbTableStat): number | undefined {
  if (!table.uncompressedBytes || table.tableBytes <= 0) return undefined
  return table.uncompressedBytes / table.tableBytes
}

export type TableSort = "name" | "rows" | "size" | "dead" | "bloat" | "scans"

const TABLE_SORTS: Record<TableSort, (table: DbTableStat) => number | string> = {
  name: (table) => `${table.schema}.${table.table}`.toLowerCase(),
  rows: (table) => table.rows,
  size: (table) => table.totalBytes,
  dead: (table) => table.deadRows,
  bloat: (table) => table.bloatBytes,
  scans: (table) => table.seqScans,
}

export function isTableSort(value: string): value is TableSort {
  return Object.hasOwn(TABLE_SORTS, value)
}

/**
 * The tables in the order asked for. A figure the engine does not keep sorts
 * last whichever way the column runs: it is not a small number.
 */
export function sortTables(
  tables: readonly DbTableStat[],
  sort: TableSort,
  descending: boolean,
): DbTableStat[] {
  const read = TABLE_SORTS[sort]
  return [...tables].sort((a, b) => {
    const [x, y] = [read(a), read(b)]
    if (typeof x === "string" || typeof y === "string") {
      const order = String(x).localeCompare(String(y))
      return descending ? -order : order
    }
    if (x < 0 || y < 0) return x < 0 && y < 0 ? 0 : x < 0 ? 1 : -1
    return descending ? y - x : x - y
  })
}

/** The schemas a list spans, by name, with how many rows of the list each holds. */
export function schemasOf(rows: readonly { schema: string }[]): { name: string; count: number }[] {
  const counts = new Map<string, number>()
  for (const row of rows) counts.set(row.schema, (counts.get(row.schema) ?? 0) + 1)
  return [...counts.entries()]
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => a.name.localeCompare(b.name))
}

/** A table by the name it is known by in its engine: with its schema where there is one. */
export function qualified(schema: string | undefined, name: string): string {
  return schema ? `${schema}.${name}` : name
}

export type IndexFlag = "invalid" | "duplicate" | "covered" | "unused"

/** What is wrong with an index, worst first. */
export function indexFlags(index: DbIndexStat): IndexFlag[] {
  const flags: IndexFlag[] = []
  if (!index.valid) flags.push("invalid")
  if (index.duplicateOf) flags.push("duplicate")
  if (index.coveredBy) flags.push("covered")
  if (index.unused) flags.push("unused")
  return flags
}

export function countFlags(indexes: readonly DbIndexStat[]): Record<IndexFlag, number> {
  const counts: Record<IndexFlag, number> = { invalid: 0, duplicate: 0, covered: 0, unused: 0 }
  for (const index of indexes) for (const flag of indexFlags(index)) counts[flag] += 1
  return counts
}

/** The bytes the flagged indexes hold: what dropping them would hand back. */
export function flaggedBytes(indexes: readonly DbIndexStat[], flag: IndexFlag): number {
  return indexes
    .filter((index) => indexFlags(index).includes(flag))
    .reduce((total, index) => total + Math.max(index.bytes, 0), 0)
}

/** What a maintenance action is run on. Nothing named is the whole database. */
export type MaintenanceTarget = { schema?: string; table?: string; index?: string }

/** One way of running one action, as a word in a menu and the request behind it. */
export type MaintenanceVerb = {
  key: string
  label: string
  action: DbMaintenanceAction
  request: DbMaintenanceRequest
  /**
   * Which answer this is, where the action has a choice in it: "passive",
   * "concurrently", "online". The action's plain form has its own word too,
   * so a surface that draws the choice as one control can name every answer.
   */
  variant?: string
}

/**
 * The actions that work on one index when given its name: the server's list
 * marks an action's scope as a table or the database, and its contract names
 * these three as the ones that also take an index.
 */
const INDEX_ACTIONS = new Set(["reindex", "reorganize", "rebuild"])

const CHECKPOINT_MODES: readonly DbCheckpointMode[] = ["truncate", "passive", "full", "restart"]

/**
 * The engine's maintenance actions that apply to a target, as verbs: only
 * those the role's capability covers, and on a protected connection only the
 * checks that change nothing. An action with a choice in it — rebuild an
 * index online or not, checkpoint in one of four modes — is one verb per
 * answer, so a menu is words and no run needs a form.
 */
export function maintenanceVerbs(
  actions: readonly DbMaintenanceAction[],
  target: MaintenanceTarget,
  who: { can: (capability: Capability) => boolean; readOnly: boolean },
): MaintenanceVerb[] {
  const verbs: MaintenanceVerb[] = []
  for (const action of actions) {
    if (target.index) {
      if (!INDEX_ACTIONS.has(action.id)) continue
    } else if (target.table ? action.scope === "database" : action.scope === "table") {
      continue
    }
    if (!who.can(action.requires)) continue
    if (who.readOnly && !action.readOnly) continue

    const base: DbMaintenanceRequest = {
      action: action.id,
      ...(target.schema ? { schema: target.schema } : {}),
      ...(target.table ? { table: target.table } : {}),
      ...(target.index ? { index: target.index } : {}),
    }
    const options = action.options ?? []
    if (options.includes("mode")) {
      for (const mode of CHECKPOINT_MODES) {
        verbs.push({
          key: `${action.id}:${mode}`,
          // The server's default is truncate; it keeps the action's own name.
          label: mode === "truncate" ? action.label : `${action.label}, ${mode}`,
          action,
          request: { ...base, options: { mode } },
          variant: mode,
        })
      }
      continue
    }
    // The plain form's word, beside the one its option adds.
    const plain = options.includes("concurrently")
      ? "blocking"
      : options.includes("online")
        ? "offline"
        : options.includes("final")
          ? "as needed"
          : undefined
    verbs.push({
      key: action.id,
      label: action.label,
      action,
      request: base,
      ...(plain ? { variant: plain } : {}),
    })
    if (options.includes("concurrently")) {
      verbs.push({
        key: `${action.id}:concurrently`,
        label: `${action.label} concurrently`,
        action,
        request: { ...base, options: { concurrently: true } },
        variant: "concurrently",
      })
    }
    if (options.includes("final")) {
      verbs.push({
        key: `${action.id}:final`,
        label: `${action.label} final`,
        action,
        request: { ...base, options: { final: true } },
        variant: "final",
      })
    }
    if (options.includes("online")) {
      verbs.push({
        key: `${action.id}:online`,
        label: `${action.label} online`,
        action,
        request: { ...base, options: { online: true } },
        variant: "online",
      })
    }
  }
  return verbs
}

/**
 * Whether this way of running an action holds its lock for the whole run. The
 * action's own mark is about its plain form: run concurrently, or online, it
 * blocks nothing but a moment at each end.
 */
export function locksThroughout(verb: MaintenanceVerb): boolean {
  const options = verb.request.options
  return Boolean(verb.action.blocking && !options?.concurrently && !options?.online)
}

/** The verbs of one action together, in the order the actions came: one group per row of a list. */
export function byAction(verbs: readonly MaintenanceVerb[]): MaintenanceVerb[][] {
  const groups = new Map<string, MaintenanceVerb[]>()
  for (const verb of verbs) {
    groups.set(verb.action.id, [...(groups.get(verb.action.id) ?? []), verb])
  }
  return [...groups.values()]
}

/** Whether a run is asked about first: it locks what it works on, or can lose rows. */
export function confirmsFirst(action: DbMaintenanceAction): boolean {
  return Boolean(action.blocking || action.destructive)
}

/** What a run works on, in words: an index, a table, or everything. `object` is the engine's word for a table. */
export function targetWords(request: DbMaintenanceRequest, object: string): string {
  if (request.index) return `index ${request.index}`
  if (request.table) return qualified(request.schema, request.table)
  if (request.schema) return `every ${object} of ${request.schema}`
  return "the whole database"
}
