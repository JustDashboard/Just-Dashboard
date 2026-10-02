import type {
  DbMaintenanceAction,
  DbMaintenanceRequest,
} from "@/components/database/ops/performance-types"

/**
 * What the Advisor decides before it draws: how many findings there are of
 * each severity and category, which ones a filter keeps, what a finding is
 * about in a few words, and — the decision that matters — whether its fix
 * may be applied from the page or only handed to the Query page for review.
 */

export type AdviceLevel = "critical" | "warning" | "notice"
export type AdviceCategory = "security" | "performance" | "reliability" | "maintenance"

export type DbAdviceTarget = {
  kind:
    | "table"
    | "index"
    | "sequence"
    | "schema"
    | "role"
    | "setting"
    | "extension"
    | "slot"
    | "object"
    | "database"
    | "server"
    | "container"
  schema?: string
  name: string
  /** The table an index belongs to, or a sequence feeds. */
  table?: string
  detail?: string
  /** The fix for this one object. */
  sql?: string
}

export type DbAdvice = {
  id: string
  level: AdviceLevel
  category: AdviceCategory
  title: string
  detail: string
  advice?: string
  objects?: string[]
  /** Every finding has at least one. */
  targets?: DbAdviceTarget[]
  /** Every target's fix, newline-joined. */
  sql?: string
  /** An absolute frontend path: "/databases/7/backups". */
  link?: string
}

export type DbEndOfLife = {
  product: string
  release: string
  date: string
  past: boolean
  daysLeft: number
}

export type DbAdviseReport = {
  checkedAt: string
  /** What was not assessed, in sentences. */
  silences: string[]
  tablesOmitted: number
  /** Worst first. */
  findings: DbAdvice[]
  tablesChecked: number
  engineChecks: boolean
  /** The schema has more than 300 tables; structure checks covered the first 300. */
  truncated: boolean
  version?: string
  endOfLife?: DbEndOfLife
}

export const LEVELS: readonly { id: AdviceLevel; label: string; one: string }[] = [
  { id: "critical", label: "Critical", one: "critical finding" },
  { id: "warning", label: "Warnings", one: "warning" },
  { id: "notice", label: "Notices", one: "notice" },
]

export const CATEGORIES: readonly { id: AdviceCategory; label: string }[] = [
  { id: "security", label: "Security" },
  { id: "reliability", label: "Reliability" },
  { id: "performance", label: "Performance" },
  { id: "maintenance", label: "Maintenance" },
]

export const isLevel = (value: string): value is AdviceLevel =>
  LEVELS.some((level) => level.id === value)

export const isCategory = (value: string): value is AdviceCategory =>
  CATEGORIES.some((category) => category.id === value)

export function countLevels(findings: readonly DbAdvice[]): Record<AdviceLevel, number> {
  const counts: Record<AdviceLevel, number> = { critical: 0, warning: 0, notice: 0 }
  for (const finding of findings) {
    if (Object.hasOwn(counts, finding.level)) counts[finding.level] += 1
  }
  return counts
}

export function countCategories(findings: readonly DbAdvice[]): Record<AdviceCategory, number> {
  const counts: Record<AdviceCategory, number> = {
    security: 0,
    reliability: 0,
    performance: 0,
    maintenance: 0,
  }
  for (const finding of findings) {
    if (Object.hasOwn(counts, finding.category)) counts[finding.category] += 1
  }
  return counts
}

/** The findings a severity and a category keep. Either may be left open. */
export function keptFindings(
  findings: readonly DbAdvice[],
  filter: { level?: AdviceLevel | null; category?: AdviceCategory | null },
): DbAdvice[] {
  return findings.filter(
    (finding) =>
      (!filter.level || finding.level === filter.level) &&
      (!filter.category || finding.category === filter.category),
  )
}

/**
 * A finding's key in the list. The advisor's ids name the check, and one
 * check can report twice, so the id alone is not one finding.
 */
export function findingKeys(findings: readonly DbAdvice[]): string[] {
  const seen = new Map<string, number>()
  return findings.map((finding) => {
    const n = seen.get(finding.id) ?? 0
    seen.set(finding.id, n + 1)
    return n === 0 ? finding.id : `${finding.id}~${n}`
  })
}

/** A target by the name it is known by: with its schema where it has one. */
export function targetName(target: DbAdviceTarget): string {
  return target.schema ? `${target.schema}.${target.name}` : target.name
}

const KIND_WORDS: Record<DbAdviceTarget["kind"], [string, string]> = {
  table: ["table", "tables"],
  index: ["index", "indexes"],
  sequence: ["sequence", "sequences"],
  schema: ["schema", "schemas"],
  role: ["account", "accounts"],
  setting: ["setting", "settings"],
  extension: ["extension", "extensions"],
  slot: ["slot", "slots"],
  object: ["object", "objects"],
  database: ["database", "databases"],
  server: ["server", "servers"],
  container: ["container", "containers"],
}

/** What a kind of target is called, in the singular or the plural. */
export function kindWord(kind: DbAdviceTarget["kind"], count = 1): string {
  const words = Object.hasOwn(KIND_WORDS, kind) ? KIND_WORDS[kind] : KIND_WORDS.object
  return count === 1 ? words[0] : words[1]
}

/**
 * What a finding is about, in the width of a row's edge: the one object by
 * its name, or how many of what — "public.audit_log", "3 tables".
 */
export function aboutWords(advice: DbAdvice): string {
  const targets = advice.targets ?? []
  if (targets.length === 1) return targetName(targets[0])
  if (targets.length > 1) {
    const kind = targets[0].kind
    const same = targets.every((target) => target.kind === kind)
    return `${targets.length} ${same ? kindWord(kind, targets.length) : "objects"}`
  }
  const objects = advice.objects ?? []
  return objects.length === 1 ? objects[0] : objects.length > 1 ? `${objects.length} objects` : ""
}

/**
 * The checks whose fix is one of the engine's own maintenance actions, and
 * which. A fix that is "vacuum this table" is run as the maintenance route
 * runs it — through the engine's closed list, with its output — rather than
 * as text in a console.
 */
const MAINTENANCE_FIXES: Record<
  string,
  { actions: string[]; options?: DbMaintenanceRequest["options"] }
> = {
  "dead-rows": { actions: ["vacuum_analyze"] },
  // Each engine's own word for gathering the planner's statistics.
  "never-analysed": { actions: ["analyze", "gather_stats"] },
  "table-free-space": { actions: ["optimize"] },
  "too-many-parts": { actions: ["optimize"] },
  "free-pages": { actions: ["vacuum"] },
  "wal-large": { actions: ["wal_checkpoint"], options: { mode: "truncate" } },
  "invalid-index": { actions: ["reindex"], options: { concurrently: true } },
}

export type MaintenanceFix = { action: DbMaintenanceAction; request: DbMaintenanceRequest }

/**
 * The maintenance run that is a target's fix, when the finding is one of
 * those checks and the engine's list has the action for what the target is: a
 * table, an index of a table, or the database as a whole.
 */
export function maintenanceFix(
  advice: Pick<DbAdvice, "id">,
  target: DbAdviceTarget,
  actions: readonly DbMaintenanceAction[],
): MaintenanceFix | undefined {
  if (!Object.hasOwn(MAINTENANCE_FIXES, advice.id)) return undefined
  const fix = MAINTENANCE_FIXES[advice.id]
  const action = actions.find((entry) => fix.actions.includes(entry.id))
  if (!action) return undefined
  const options = fix.options
    ? Object.fromEntries(
        Object.entries(fix.options).filter(([name]) =>
          (action.options ?? []).includes(
            name as NonNullable<DbMaintenanceAction["options"]>[number],
          ),
        ),
      )
    : {}
  const withOptions = Object.keys(options).length > 0 ? { options } : {}
  if (target.kind === "table") {
    if (action.scope === "database") return undefined
    return {
      action,
      request: {
        action: action.id,
        ...(target.schema ? { schema: target.schema } : {}),
        table: target.name,
        ...withOptions,
      },
    }
  }
  if (target.kind === "index") {
    if (!target.table) return undefined
    return {
      action,
      request: {
        action: action.id,
        ...(target.schema ? { schema: target.schema } : {}),
        table: target.table,
        index: target.name,
        ...withOptions,
      },
    }
  }
  if (target.kind === "database" || target.kind === "server") {
    if (action.scope === "table") return undefined
    return { action, request: { action: action.id, ...withOptions } }
  }
  return undefined
}

/** How a fix can be carried out from the Advisor, and why not when it cannot. */
export type ApplyVerdict =
  | { apply: true; via: "maintenance"; fix: MaintenanceFix }
  | { apply: true; via: "statement" }
  | { apply: false; reason: string }

/**
 * Whether a fix is applied from here.
 *
 * Only where the server itself marks it safe, in one of two ways. Its
 * maintenance list marks an action that neither locks what it works on nor
 * can lose rows (`requires: "service.control"`): a vacuum, an analyze, a
 * checkpoint. And its classifier — the verdict the Query page runs under —
 * marks a statement as not destructive: an index to create. Everything else
 * — a table rewritten to gain a key, an index dropped, a server setting
 * changed — is the reader's to review and run on the Query page, where the
 * confirmation that fits it lives.
 *
 * A protected connection applies nothing, and neither does a role that may
 * not control the service.
 */
export function applyVerdict(input: {
  /** The maintenance run this fix is, where it is one. */
  maintenance?: MaintenanceFix
  /** The server's verdict on the statement; `undefined` while it has not answered. */
  destructive?: boolean
  readOnly: boolean
  mayControl: boolean
}): ApplyVerdict {
  if (input.readOnly) {
    return { apply: false, reason: "This connection is protected: nothing is changed from here." }
  }
  if (!input.mayControl) {
    return { apply: false, reason: "Your role may read the fix and not run it." }
  }
  const fix = input.maintenance
  if (fix) {
    if (fix.action.requires === "service.control") return { apply: true, via: "maintenance", fix }
    return {
      apply: false,
      reason: fix.action.destructive
        ? "This fix can lose rows, so it is not applied from here."
        : "This fix locks what it works on while it runs, so it is not applied from here. It is run from Performance, where it is asked about first.",
    }
  }
  if (input.destructive === false) return { apply: true, via: "statement" }
  if (input.destructive === true) {
    return {
      apply: false,
      reason:
        "The server does not class this statement as safe to run unread, so it is reviewed and run on the Query page.",
    }
  }
  return { apply: false, reason: "" }
}

/**
 * Whether a statement builds an index the plain way: `CREATE INDEX` with
 * nothing said about doing it alongside the table's writes. The classifier
 * calls it harmless, and it destroys nothing — but on an engine that also
 * has a concurrent build, the plain one holds every write to the table until
 * it is done.
 */
export function buildsIndex(sql: string): boolean {
  return /^\s*create\s+(?:unique\s+)?index\b/i.test(sql) && !/\bconcurrently\b/i.test(sql)
}

/** The same index built alongside the table's writes. */
export function concurrently(sql: string): string {
  return buildsIndex(sql)
    ? sql.replace(/^(\s*create\s+(?:unique\s+)?index)\b/i, "$1 CONCURRENTLY")
    : sql
}

/** One statement of a fix, without the semicolon that ends it. */
export function statementsOf(sql: string): string[] {
  return sql
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}
