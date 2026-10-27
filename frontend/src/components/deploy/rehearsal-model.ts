import type { DeploymentVariable, RehearsalPolicy, RehearsalReport } from "@/lib/types"
import { plural } from "@/lib/format"

/** The terms an environment has before anyone saves its own (`DefaultRehearsalPolicy`). */
export const DEFAULT_REHEARSAL_POLICY: RehearsalPolicy = {
  enabled: false,
  connectionId: 0,
  whenUnavailable: "block",
  restoreTimeoutSeconds: 600,
  maxBackupMb: 2048,
}

/** "850 ms", "2.0 s", "1m 12s": a rehearsal's times are seconds more often than minutes. */
export function millis(ms: number | undefined) {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return "—"
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`
  const seconds = Math.round(ms / 1000)
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}

/**
 * The step's evidence as a report, or nothing when the step only recorded why
 * it was skipped (no release tasks, rehearsal turned off).
 */
export function rehearsalOf(evidence: Record<string, unknown> | undefined) {
  if (!evidence || !Array.isArray(evidence.tasks)) return undefined
  if (typeof evidence.locks !== "object" || evidence.locks === null) return undefined
  return evidence as unknown as RehearsalReport
}

const ENGINE_NAMES: Record<string, string> = {
  postgres: "PostgreSQL",
  mysql: "MySQL",
  mariadb: "MariaDB",
}

export function engineName(engine: string | undefined) {
  return (engine && ENGINE_NAMES[engine]) ?? engine ?? "database"
}

/** "AccessExclusiveLock" as PostgreSQL's documentation names it: "ACCESS EXCLUSIVE". */
export function lockMode(mode: string) {
  return mode
    .replace(/Lock$/, "")
    .replace(/([a-z])([A-Z])/g, "$1 $2")
    .toUpperCase()
}

/** Where the copy came from, as the end of "restored from …". */
export function sourceWords(source: RehearsalReport["source"], format: (iso: string) => string) {
  if (!source) return "a backup"
  if (source.kind === "pitr") {
    return source.takenAt
      ? `the point-in-time archive, to ${format(source.takenAt)}`
      : "the point-in-time archive"
  }
  return source.takenAt ? `the backup of ${format(source.takenAt)}` : "the newest backup"
}

/** Where the copy came from, short enough for a figure's line: "dump of 03:15". */
export function sourceShort(source: RehearsalReport["source"], format: (iso: string) => string) {
  if (!source) return "a backup"
  const at = source.takenAt ? format(source.takenAt) : undefined
  if (source.kind === "pitr") return at ? `archive to ${at}` : "the archive"
  return at ? `dump of ${at}` : "newest dump"
}

/** The modes the copy's lock table is read for, strongest first. */
const LOCK_STRENGTH = ["AccessExclusiveLock", "ExclusiveLock", "ShareRowExclusiveLock", "ShareLock"]

function strength(mode: string) {
  const at = LOCK_STRENGTH.indexOf(mode)
  return at < 0 ? LOCK_STRENGTH.length : at
}

/**
 * The locks worth reading: a weaker mode on a table that a stronger one held
 * at least as long says nothing more — an ALTER TABLE and the CREATE INDEX
 * in its transaction both lock it, and the ALTER's lock is the one that
 * matters. Longest first, the stronger first among equals.
 */
export function heldLocks(report: RehearsalReport) {
  const held = report.locks.held ?? []
  return held
    .filter(
      (lock) =>
        !held.some(
          (other) =>
            other.relation === lock.relation &&
            strength(other.mode) < strength(lock.mode) &&
            other.heldMs >= lock.heldMs,
        ),
    )
    .sort((a, b) => b.heldMs - a.heldMs || strength(a.mode) - strength(b.mode))
}

/** The lock held longest, the one a reader of the report wants first. */
export function longestLock(report: RehearsalReport) {
  return heldLocks(report)[0]
}

/** How the release tasks went against the copy, in a few words. */
export function tasksLine(report: RehearsalReport) {
  const failed = report.tasks.find((task) => task.exitCode !== 0 || task.error)
  if (failed) return `${failed.name} failed (exit ${failed.exitCode})`
  if (report.tasks.length === 0) return "no task ran"
  return report.tasks.length === 1
    ? `${report.tasks[0].name} passed`
    : `${plural(report.tasks.length, "task")} passed`
}

/**
 * The step row's line while another step is open: what was restored, how the
 * tasks went and the longest lock — the three things the rehearsal is for.
 */
export function rehearsalSummary(report: RehearsalReport) {
  if (!report.rehearsed) return report.reason ? `Not rehearsed: ${report.reason}` : undefined
  const lock = longestLock(report)
  return [
    report.database?.name,
    tasksLine(report),
    lock && `${lock.relation} locked ${millis(lock.heldMs)}`,
  ]
    .filter(Boolean)
    .join(" · ")
}

/** The settings head's line: what a release does now. */
export function rehearsalPolicyLine(policy: RehearsalPolicy) {
  if (!policy.enabled) return "Off · release tasks run against production directly"
  return policy.whenUnavailable === "warn"
    ? "On · a release that cannot be rehearsed continues with a warning"
    : "On · a release that cannot be rehearsed is stopped"
}

/**
 * The databases the release tasks reach: those a variable available to
 * release tasks references (`${{database.5.url}}` reads connection 5) — the
 * same ones the server picks between (`rehearsalDatabaseIDs`).
 */
export function rehearsalCandidates(variables: DeploymentVariable[]) {
  const ids = new Set<number>()
  for (const variable of variables) {
    if (variable.reference?.kind !== "database") continue
    if (!variable.scopes.includes("release_task")) continue
    const id = Number(variable.reference.target.split(".")[0])
    if (Number.isInteger(id) && id > 0) ids.add(id)
  }
  return [...ids].sort((a, b) => a - b)
}
