import { bytes, plural, relativeTime } from "@/lib/format"
import type { Job, JobLine } from "@/lib/types"
import { transferResult } from "@/components/database/ops/backups-model"

/**
 * What the Version page reads and says about a major-version upgrade: the
 * routes' answers as the server writes them, and the sentences made from
 * them. No React, so each sentence can be held to its inputs.
 */

/** One release a server can be moved to: its id in the closed list, and the image. */
export type DbUpgradeTarget = { version: string; image: string }

/** What a check found about one thing. */
export type DbUpgradeVerdict = "ok" | "warning" | "blocker"

export type DbUpgradeCheck = {
  id: string
  verdict: DbUpgradeVerdict
  title: string
  detail: string
  /** The names the detail is about. */
  items?: string[]
}

/** The last `result` line of a check: the preflight report. */
export type DbUpgradeReport = {
  from: string
  to: string
  /** The two servers' own versions: "16.15", "17.11". */
  server: string
  target: string
  image: string
  bytes: number
  databases: { name: string; bytes: number; template: boolean }[]
  checks: DbUpgradeCheck[]
  /** No check stops the upgrade. */
  ready: boolean
  checkedAt: string
}

/** The last `result` line of an upgrade. */
export type DbUpgradeResult = {
  from: string
  to: string
  server: string
  container: string
  previous: string
  volume: string
  safetyDump?: string
  databases: number
  tables: number
  rows: number
  summary: string
}

export type DbUpgradeState = "running" | "upgraded" | "finished" | "reverted" | "failed"

/** One upgrade of this server, as `GET /databases/{id}/upgrade` describes it. */
export type DbUpgradeRecord = {
  id: number
  state: DbUpgradeState
  phase?: string
  engine: string
  fromVersion: string
  toVersion: string
  fromImage: string
  toImage: string
  /** The name the server answers under, before and after. */
  container: string
  /** Where the old container is kept. */
  previous?: string
  /** What Docker says of the kept container now; `missing` once it is gone. */
  previousState?: string
  previousVolume?: string
  volume?: string
  safetyDump?: string
  error?: string
  startedBy?: string
  startedAt: string
  upgradedAt?: string
  endedAt?: string
}

/** `GET /databases/{id}/upgrade`. */
export type DbUpgrade = {
  /** The server can be upgraded from here now. */
  offered: boolean
  /** Why it cannot, as a sentence to show as it is. */
  reason?: string
  engine?: string
  label?: string
  container?: string
  /** The release it runs, as the closed list names it. */
  version?: string
  image?: string
  volume?: string
  /** The newer releases, newest first. */
  targets: DbUpgradeTarget[]
  /** The upgrade still running, or done with its old server kept. */
  window?: DbUpgradeRecord
  /** The last upgrade that ended. */
  last?: DbUpgradeRecord
  /** A check, an upgrade or a revert running now. */
  job?: Job
}

/** A reading of the route in the shape this page reads. */
export function isUpgradeReading(value: unknown): value is DbUpgrade {
  return (
    value !== null &&
    typeof value === "object" &&
    typeof (value as DbUpgrade).offered === "boolean" &&
    Array.isArray((value as DbUpgrade).targets)
  )
}

export type UpgradeJobKind = "check" | "upgrade" | "revert"

/** Which of the three an upgrade's job is, read off the end of its kind. */
export function upgradeJobKind(job: Pick<Job, "kind">): UpgradeJobKind | undefined {
  const word = job.kind.slice(job.kind.lastIndexOf(".") + 1)
  if (word === "major-upgrade") return "upgrade"
  if (word === "major-upgrade-check") return "check"
  if (word === "major-upgrade-revert") return "revert"
  return undefined
}

/** A check's report, from its job's last line; nothing for a job that has not got that far. */
export function reportOf(lines: readonly Pick<JobLine, "stream" | "text">[]) {
  const result: unknown = transferResult(lines)
  const report = result as DbUpgradeReport | undefined
  return report && Array.isArray(report.checks) && typeof report.ready === "boolean"
    ? report
    : undefined
}

/** An upgrade's result, from its job's last line. */
export function upgradeResultOf(lines: readonly Pick<JobLine, "stream" | "text">[]) {
  const result: unknown = transferResult(lines)
  const done = result as DbUpgradeResult | undefined
  return done && typeof done.previous === "string" && typeof done.to === "string" ? done : undefined
}

/** The checks that stop the upgrade. */
export function blockers(report: DbUpgradeReport): DbUpgradeCheck[] {
  return report.checks.filter((check) => check.verdict === "blocker")
}

/** What a report comes to, in the words a section's head carries. */
export function reportWords(report: DbUpgradeReport | undefined): string {
  if (!report) return "Not checked yet"
  const when = relativeTime(report.checkedAt)
  const stopping = blockers(report).length
  if (stopping > 0) return `Checked ${when} · ${plural(stopping, "thing", "things")} to fix first`
  const notes = report.checks.filter((check) => check.verdict === "warning").length
  if (notes > 0) return `Checked ${when} · ready, ${plural(notes, "thing", "things")} to know`
  return `Checked ${when} · ready`
}

/** What happens when the upgrade runs, for the page and the confirmation alike. */
export function upgradeSteps(
  words: { label: string; from: string; to: string; name: string; database: string },
  report?: DbUpgradeReport,
): string[] {
  const size = report ? `: ${bytes(report.bytes)} of databases` : ""
  return [
    `A dump of ${words.database} is taken first and kept with its other dumps.`,
    `${words.label} ${words.to} is started beside it, and every database, role and parameter is copied into it${size}.`,
    `While the copy is made, ${words.name} takes reads only: writes are refused and the sessions connected now are ended. For a few seconds at the end it does not answer at all, while ${words.label} ${words.to} takes its name, port and password.`,
    `${words.label} ${words.from} is kept, stopped, with its data, until you finish the upgrade or revert it.`,
  ]
}

/** What the kept server is, for the head of its section. */
export function keptWords(record: DbUpgradeRecord): string {
  const when = record.upgradedAt ? ` ${relativeTime(record.upgradedAt)}` : ""
  const who = record.startedBy ? ` by ${record.startedBy}` : ""
  return `Upgraded${when}${who}`
}

/** Whether the kept server is still one to go back to, and why not. */
export function keptProblem(record: DbUpgradeRecord): string | undefined {
  const name = record.previous ?? "The old server"
  if (record.previousState === "missing") {
    return `${name} is no longer on this machine, so there is nothing to revert to. Finish the upgrade to close it.`
  }
  if (record.previousState === "running") {
    return `${name} has been started by hand. Stop it before you finish the upgrade or revert it.`
  }
  return undefined
}

/** The last upgrade, when it is worth a line: one that failed or was reverted. */
export function lastWords(
  last: DbUpgradeRecord | undefined,
  label: string,
): { tone: "danger" | "default"; title: string; body: string } | undefined {
  if (!last || !last.endedAt) return undefined
  const when = relativeTime(last.endedAt)
  if (last.state === "failed") {
    return {
      tone: "danger",
      title: `The upgrade to ${label} ${last.toVersion} failed ${when} and was undone`,
      body: last.error
        ? `${last.error}${last.safetyDump ? ` The safety dump ${last.safetyDump} is kept with the other dumps.` : ""}`
        : "Nothing was changed.",
    }
  }
  if (last.state === "reverted") {
    return {
      tone: "default",
      title: `Reverted ${when} to ${label} ${last.fromVersion}`,
      body: `${label} ${last.toVersion} was removed with what was written to it after the upgrade.`,
    }
  }
  return undefined
}

/** The toast an ended upgrade job is announced with. */
export function endedWords(
  kind: UpgradeJobKind | undefined,
  job: Pick<Job, "status" | "error">,
  name: string,
): { tone: "success" | "error" | "info"; title: string; description?: string } {
  const what =
    kind === "revert" ? "The revert" : kind === "check" ? "The check" : `The upgrade of ${name}`
  if (job.status === "succeeded") {
    return {
      tone: "success",
      title:
        kind === "revert"
          ? `${name} is back on its old release`
          : kind === "check"
            ? `${name} is checked`
            : `${name} is upgraded`,
    }
  }
  if (job.status === "failed") {
    return { tone: "error", title: `${what} failed`, description: job.error ?? undefined }
  }
  return { tone: "info", title: `${what} was stopped` }
}
