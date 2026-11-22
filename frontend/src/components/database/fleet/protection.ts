import type { DbAlertRule, DbAlertSubject } from "@/components/database/ops/alerts-types"
import type { BackupJob, DbAlertKind, DbFleetEntry, NotificationChannel } from "@/lib/types"

/**
 * What the control center says under its identity line, worked out apart
 * from its drawing: whether every database is on a backup schedule, and
 * whether anything would say so when one goes down. These two lines took the
 * place of the band of storage, sessions and backups (0.9.1): the band split
 * totals the identity line already gave across the databases, and the one
 * problem it showed — databases never backed up — it showed as an amber span
 * with nothing to press. Each line here is the reading and, when it is not
 * the expected answer, the one press that makes it so.
 */

/** The job the Schedule press makes when no database-only job is there to take the rest. */
export const NIGHTLY = {
  name: "Databases",
  /** Every night at three, the backup form's own default. */
  schedule: "0 3 * * *",
  /** The last seven archives, the form's default. */
  retention: 7,
  /** The form's default directory on this server. */
  path: "/var/backups/just-dashboard",
} as const

/** The alert kinds the Turn on press watches every database for. */
export const WATCHED_KINDS: DbAlertKind[] = [
  "down",
  "disk",
  "connections",
  "backup_failed",
  "backup_missed",
]

/** A job that runs by itself: switched on, on a schedule, and this server's own. */
export function runsByItself(job: BackupJob): boolean {
  return job.enabled && !job.imported && job.schedule.trim() !== ""
}

/** A job that holds only database dumps: the one place a database can be added without carrying files along. */
function onlyDumps(job: BackupJob): boolean {
  return (
    job.sources.length === 0 &&
    (job.sqlitePaths?.length ?? 0) === 0 &&
    (job.databaseDumps?.length ?? 0) > 0
  )
}

const nextOf = (job: BackupJob) => Date.parse(job.nextRun ?? "") || Infinity

export type BackupCover = {
  /** The databases the dashboard can dump, in the fleet's order. */
  dumpable: DbFleetEntry[]
  /** Those no job dumps by itself. */
  uncovered: DbFleetEntry[]
  /** The jobs that dump one of them by itself, the next to run first. */
  jobs: BackupJob[]
  /** One of those whose last run failed. */
  failed?: BackupJob
  /** One of those running now. */
  running?: BackupJob
  /**
   * The database-only job the uncovered ones would join, the one that dumps
   * the most of the fleet. Absent when there is none, and a new job is made.
   */
  home?: BackupJob
}

/**
 * Which of the databases a backup job dumps on a schedule. A paused job, one
 * that only runs by hand and one taken on from another server's destination
 * protect nothing from here, so their databases count as not covered.
 * A database the dashboard cannot dump, or a row that cannot be opened, is
 * left out rather than counted against the server.
 */
export function backupCover(
  entries: readonly DbFleetEntry[],
  jobs: readonly BackupJob[],
  dumps: (entry: DbFleetEntry) => boolean,
): BackupCover {
  const dumpable = entries.filter(
    (entry) => !entry.broken && entry.state !== "broken" && dumps(entry),
  )
  const ids = new Set(dumpable.map((entry) => entry.id))
  const scheduled = jobs.filter(runsByItself)
  const covering = scheduled
    .filter((job) => job.databaseDumps?.some((id) => ids.has(id)))
    .sort((a, b) => nextOf(a) - nextOf(b))
  const covered = new Set(covering.flatMap((job) => job.databaseDumps ?? []))
  const held = (job: BackupJob) => (job.databaseDumps ?? []).filter((id) => ids.has(id)).length
  const home = scheduled
    .filter(onlyDumps)
    .sort((a, b) => held(b) - held(a) || nextOf(a) - nextOf(b))[0]
  return {
    dumpable,
    uncovered: dumpable.filter((entry) => !covered.has(entry.id)),
    jobs: covering,
    failed: covering.find((job) => job.lastRun?.status === "failed"),
    running: covering.find((job) => job.lastRun?.status === "running"),
    home,
  }
}

/** The body of `POST /backups/` for the job the Schedule press makes. */
export function nightlyJob(ids: readonly number[]) {
  return {
    name: NIGHTLY.name,
    sources: [],
    excludes: [],
    targetKind: "local" as const,
    target: { path: NIGHTLY.path },
    schedule: NIGHTLY.schedule,
    retention: NIGHTLY.retention,
    retentionDays: 0,
    enabled: true,
    databaseDumps: [...ids],
  }
}

export type AlertCover = {
  /** The databases that can be opened, in the fleet's order. */
  watchable: DbFleetEntry[]
  /** Those no switched-on rule watches. */
  unwatched: DbFleetEntry[]
  /** The kinds the Turn on press would add: those with no rule over every database yet. */
  missing: DbAlertKind[]
  /** What is firing now, the first a database's own. */
  firing: DbAlertSubject[]
  /** The switched-on channels the rules reach; undefined while they are not known. */
  reached?: NotificationChannel[]
}

/**
 * Which databases an alert rule watches. A rule with no database watches
 * every one whose engine has its kind, and is the one the Turn on press adds;
 * a rule somebody paused still counts as there, so the press never adds a
 * second beside it. A rule tells the channels it names, or every one switched
 * on when it names none.
 */
export function alertCover(
  entries: readonly DbFleetEntry[],
  rules: readonly DbAlertRule[],
  subjects: readonly DbAlertSubject[],
  channels: readonly NotificationChannel[] | undefined,
): AlertCover {
  const watchable = entries.filter((entry) => !entry.broken && entry.state !== "broken")
  const on = rules.filter((rule) => rule.enabled)
  const everyDatabase = on.some((rule) => rule.connectionId === null)
  const own = new Set(on.map((rule) => rule.connectionId))
  const unwatched = everyDatabase ? [] : watchable.filter((entry) => !own.has(entry.id))
  const fleetKinds = new Set(
    rules.filter((rule) => rule.connectionId === null).map((rule) => rule.kind),
  )
  const live = new Set(watchable.map((entry) => entry.id))
  const firing = subjects
    .filter((subject) => subject.state === "firing")
    .filter((subject) => subject.connectionId === undefined || live.has(subject.connectionId))
    .sort((a, b) => Number(a.connectionId === undefined) - Number(b.connectionId === undefined))
  let reached: NotificationChannel[] | undefined
  if (channels) {
    const named = new Set(on.flatMap((rule) => rule.channels))
    const toldAll = on.some((rule) => rule.channels.length === 0)
    reached = channels.filter((channel) => channel.enabled && (toldAll || named.has(channel.id)))
  }
  return {
    watchable,
    unwatched,
    missing: WATCHED_KINDS.filter((kind) => !fleetKinds.has(kind)),
    firing,
    reached,
  }
}

/** Names in a line: "shop, blog and cache", or the first few and how many more. */
export function namesLine(names: readonly string[], shown = 3): string {
  if (names.length === 0) return ""
  if (names.length === 1) return names[0]
  if (names.length <= shown) return `${names.slice(0, -1).join(", ")} and ${names.at(-1)}`
  return `${names.slice(0, shown).join(", ")} and ${names.length - shown} more`
}
