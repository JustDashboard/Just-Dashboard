import { plural } from "@/lib/format"
import type { DotTone } from "@/components/status-dot"
import type {
  DbPitr,
  PitrArchive,
  PitrTargetKind,
  RecoveryTarget,
  WalWindow,
} from "@/components/database/ops/pitr-types"

/**
 * What the Backups page says about a server's point-in-time recovery, worked
 * out from what the server answers: whether it archives and how well, what
 * moments it can go back to, and what a form is about to ask for. No React,
 * so each sentence can be held to its inputs.
 */

/** The kind prefix every point-in-time recovery job of one server runs under. */
export function pitrJobPrefix(container: string): string {
  return `database.pitr.${container}.`
}

/** How long a form waits after a change before it asks the server what will run. */
export const PLAN_AFTER_MS = 400

/** Finished segments past which an archive is said to be falling behind. */
export const BEHIND_AFTER = 3

export type ArchiveHealth = {
  tone: DotTone
  /** The state, in a word or two. */
  word: string
  /** What is wrong, for a state that is not simply "archiving". */
  detail?: string
}

/** Whether a server is archiving, read from what it and the page know. */
export function archiveHealth(pitr: DbPitr): ArchiveHealth {
  if (!pitr.archive) return { tone: "stopped", word: "Off" }
  if (pitr.drift) {
    return {
      tone: "danger",
      word: "Not archiving",
      detail:
        "The container was made again without the archive's settings, so nothing reaches the archive. Turn point-in-time recovery off and set it up again.",
    }
  }
  if (!pitr.running) {
    return {
      tone: "stopped",
      word: "Server stopped",
      detail: "Nothing is written, so nothing is archived.",
    }
  }
  const archiver = pitr.archiver
  if (!archiver) return { tone: "unknown", word: "Not read" }
  if (failing(archiver.lastArchivedAt, archiver.lastFailedAt)) {
    return {
      tone: "danger",
      word: "Failing",
      detail:
        pitr.archiveError ??
        `Segment ${archiver.lastFailed ?? ""} could not be archived; the server keeps it and tries again.`,
    }
  }
  if ((archiver.waiting ?? 0) > BEHIND_AFTER) {
    return {
      tone: "notice",
      word: "Behind",
      detail: `${plural(archiver.waiting ?? 0, "finished segment is", "finished segments are")} waiting to be archived.`,
    }
  }
  return { tone: "running", word: "Archiving" }
}

function failing(archivedAt?: string, failedAt?: string): boolean {
  if (!failedAt) return false
  if (!archivedAt) return true
  return Date.parse(failedAt) > Date.parse(archivedAt)
}

/** A moment as the page writes one: the date and the time to the second. */
export function momentWords(iso: string | undefined): string {
  if (!iso) return ""
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })
}

/** What an archive covers, as one sentence. */
export function windowWords(window: WalWindow): string {
  if (!window.from) return "No moment yet: the first base backup has not finished."
  return `Any moment from ${momentWords(window.from)} to ${momentWords(window.to ?? window.from)}`
}

/** Where an archive is kept, as the operator would look for it. */
export function locationWords(
  archive: Pick<PitrArchive, "targetKind" | "target" | "stream">,
): string {
  if (archive.targetKind === "local") {
    return `${(archive.target.path ?? "").replace(/\/+$/, "")}/${archive.stream}`
  }
  const parts = [archive.target.bucket, archive.target.prefix, archive.stream]
    .map((part) => (part ?? "").replace(/^\/+|\/+$/g, ""))
    .filter(Boolean)
  return `s3://${parts.join("/")}`
}

export const TARGET_LABEL: Record<PitrTargetKind, string> = {
  local: "A directory on this server",
  s3: "Amazon S3 or any S3-compatible bucket",
  b2: "Backblaze B2",
}

/** How long an archive keeps what it holds. */
export function keepsWords(days: number): string {
  return days > 0 ? `the last ${plural(days, "day")}` : "everything"
}

/** A command line as a shell would need it written, for reading. */
export function commandLine(argv: readonly string[]): string {
  return argv
    .map((arg) => (/^[\w./:=@%+,-]+$/.test(arg) ? arg : `'${arg.replace(/'/g, "'\\''")}'`))
    .join(" ")
}

/** Server settings as postgresql.conf would hold them. */
export function settingsText(settings: Record<string, string>): string {
  return Object.keys(settings)
    .sort()
    .map((name) => `${name} = '${settings[name].replace(/'/g, "''")}'`)
    .join("\n")
}

/** A moment's value for a `datetime-local` field, in this browser's time. */
export function toLocalInput(iso: string | undefined): string {
  if (!iso) return ""
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** A `datetime-local` field's value as the moment it names. */
export function fromLocalInput(value: string): string | undefined {
  if (!value) return undefined
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

/** Why a recovery target cannot be asked for, or nothing. */
export function targetProblem(
  target: RecoveryTarget,
  window: WalWindow,
  now = Date.now(),
): string | undefined {
  if (target.kind === "time") {
    if (!target.time) return "Choose the moment to recover to."
    const at = Date.parse(target.time)
    if (window.from && at < Date.parse(window.from)) {
      return `The archive goes back to ${momentWords(window.from)}, no earlier.`
    }
    if (at > now + 60_000) return "That moment has not come yet."
  }
  if (target.kind === "name") {
    if (!target.name) return "Choose the restore point to recover to."
    if (!RESTORE_POINT.test(target.name)) return RESTORE_POINT_RULE
  }
  return undefined
}

/** What the server takes for a restore point's name. */
export const RESTORE_POINT = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,62}$/
export const RESTORE_POINT_RULE =
  "1 to 63 letters, digits and . _ : -, starting with a letter or a digit."

/** A recovery target in words: what a copy was, or will be, recovered to. */
export function targetWords(target: RecoveryTarget, recoveredTo?: string): string {
  if (recoveredTo) return momentWords(recoveredTo)
  if (target.kind === "time") return momentWords(target.time)
  if (target.kind === "name") return `restore point ${target.name ?? ""}`
  return "the latest moment"
}

/** A time target past the end of the archive is recovered to the end, which is the same state. */
export function pastTheEnd(target: RecoveryTarget, window: WalWindow): boolean {
  return (
    target.kind === "time" &&
    Boolean(target.time && window.to) &&
    Date.parse(target.time!) > Date.parse(window.to!)
  )
}

export type SetupForm = {
  source: PitrTargetKind | "job"
  path: string
  bucket: string
  endpoint: string
  accessKey: string
  secretKey: string
  fromJob?: number
  retentionDays: string
}

/** Why the setup form cannot be sent yet, or nothing. */
export function setupProblem(form: SetupForm): string | undefined {
  switch (form.source) {
    case "local":
      if (!form.path.trim().startsWith("/")) return "Name the directory by its full path."
      break
    case "s3":
    case "b2":
      if (!form.bucket.trim()) return "Name the bucket."
      if (form.source === "b2" && !form.endpoint.trim())
        return "Backblaze B2 needs the bucket's endpoint."
      if (Boolean(form.accessKey.trim()) !== Boolean(form.secretKey.trim())) {
        return "Give both keys, or neither for the server's own role."
      }
      break
    case "job":
      if (!form.fromJob) return "Choose the backup job."
      break
  }
  const days = Number(form.retentionDays)
  if (!Number.isInteger(days) || days < 0 || days > 3650) return "Keep between 0 and 3650 days."
  return undefined
}
