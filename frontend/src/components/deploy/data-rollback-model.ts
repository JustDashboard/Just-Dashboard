import type {
  DataCopy,
  DataCopyComparison,
  DataCopyState,
  DeploymentRelease,
  ReleaseDataPoint,
  ReleaseListChange,
} from "@/lib/types"
import type { DotTone } from "@/components/status-dot"
import { plural } from "@/lib/format"

/**
 * Data-aware rollback, as words: where a linked database stood when a release
 * went live, the side copy restored from that point, and the two ways on from
 * it. Pure, so the wording and the links are unit-tested here and the
 * components only lay them out.
 */

/** How long a copy is kept once it is ready (`deploy.DefaultDataCopyLifetime`). */
export const DATA_COPY_HOURS = 2

/**
 * "2026-09-03 12:00:00 UTC". A data point is a moment the archive recovers to
 * or the start of a backup run, so it is written to the second and in UTC:
 * it is what the Backups page's recovery dialog is prefilled with, and the
 * same instant must read the same in both places whatever the browser's zone.
 */
export function utcMoment(iso: string | undefined, seconds = true) {
  if (!iso) return ""
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const text = d.toISOString().replace("T", " ")
  return `${seconds ? text.slice(0, 19) : text.slice(0, 16)} UTC`
}

/** One point in the reader's words: "point in time · 2026-09-03 12:00:00 UTC". */
export function dataPointWords(
  point: Pick<ReleaseDataPoint, "kind" | "at" | "backupRunId" | "reason">,
) {
  if (point.kind === "pitr") {
    return point.at ? `point in time · ${utcMoment(point.at)}` : "point in time"
  }
  if (point.kind === "dump") {
    const when = point.at ? `backup of ${utcMoment(point.at, false)}` : "a backup"
    return point.backupRunId ? `${when}, run ${point.backupRunId}` : when
  }
  return point.reason || "no backup to restore from"
}

/** The points a copy can be restored from: a recorded "none" is said, never restored. */
export function restorablePoints(points: ReleaseDataPoint[] | undefined) {
  return (points ?? []).filter((point) => point.kind !== "none")
}

/**
 * The release row's tag, and its title: the one line a reader hovers for —
 * every linked database with its point, one to a line.
 */
export function dataPointTag(release: Pick<DeploymentRelease, "dataPoints">) {
  const points = release.dataPoints ?? []
  if (points.length === 0) return undefined
  const restorable = restorablePoints(points).length > 0
  return {
    restorable,
    title: points.map((point) => `${point.name}: ${dataPointWords(point)}`).join("\n"),
  }
}

/**
 * Where "Restore in place to this point" goes: the existing route that owns
 * the destructive half, with the recorded point already chosen. A recovery to
 * the moment on the database's Backups page (into a new server, which is then
 * switched in there), or the backup run's own "Restore database" with this
 * database's dump. Nothing when the point lacks what the route needs.
 */
export function restoreInPlaceLink(
  connectionId: number,
  source: Pick<ReleaseDataPoint, "kind" | "at" | "backupRunId" | "backupJobId">,
) {
  if (source.kind === "pitr" && source.at && connectionId > 0) {
    return `/databases/${connectionId}/backups?recover=${encodeURIComponent(source.at)}`
  }
  if (source.kind === "dump" && source.backupJobId && source.backupRunId && connectionId > 0) {
    return `/backups/${source.backupJobId}?run=${source.backupRunId}&restore=${connectionId}`
  }
  return undefined
}

/** What the restore-in-place route does there, said before the reader leaves for it. */
export function restoreInPlaceWords(kind: ReleaseDataPoint["kind"]) {
  return kind === "pitr"
    ? "Recovers the server to this moment on its Backups page; switching the recovered server in for production is destructive and asks for confirmation there."
    : "Opens the backup run's database restore, which replaces production's data with this dump and asks for confirmation there."
}

/** The sentence every surface says about the third way that is not offered. */
export const NOT_POINTED =
  "Pointing the rolled-back release at the copy is not offered: production must never run against a throwaway copy that is removed when it expires."

const ACTIVE: DataCopyState[] = ["waiting", "restoring", "ready"]

/** A copy that is still the operator's: waiting for its rollback, restoring, or ready. */
export function copyActive(state: DataCopyState) {
  return ACTIVE.includes(state)
}

/** A copy whose next reading is worth having soon. */
export function copyBusy(state: DataCopyState) {
  return state === "waiting" || state === "restoring"
}

/** How often the copies are read: every five seconds while one is moving, else thirty. */
export function copiesPollMs(copies: DataCopy[] | undefined) {
  return copies?.some((copy) => copyBusy(copy.state)) ? 5_000 : 30_000
}

/** "1h 59m", "12m", "under a minute": what is left of a copy's life. */
export function remaining(expiresAt: string | undefined, now: number) {
  if (!expiresAt) return ""
  const left = Math.floor((Date.parse(expiresAt) - now) / 60_000)
  if (!Number.isFinite(left)) return ""
  if (left < 1) return "under a minute"
  const hours = Math.floor(left / 60)
  const minutes = left % 60
  if (hours === 0) return `${minutes}m`
  return minutes === 0 ? `${hours}h` : `${hours}h ${minutes}m`
}

/** A copy's state as `Status` draws it: the tone and the word. */
export function copyStatus(copy: Pick<DataCopy, "state">): { tone: DotTone; label: string } {
  switch (copy.state) {
    case "waiting":
      return { tone: "notice", label: "Waiting for the rollback" }
    case "restoring":
      return { tone: "notice", label: "Restoring" }
    case "ready":
      return { tone: "running", label: "Ready" }
    case "failed":
      return { tone: "danger", label: "Failed" }
    case "skipped":
      return { tone: "stopped", label: "Not restored" }
    default:
      return { tone: "stopped", label: "Removed" }
  }
}

/** What a copy holds, as a point: the moment it reached, or the backup it came from. */
export function copySourceWords(copy: Pick<DataCopy, "source" | "recoveredTo">) {
  if (copy.source.kind === "pitr") {
    return dataPointWords({ kind: "pitr", at: copy.recoveredTo ?? copy.source.at })
  }
  return dataPointWords({ kind: "dump", at: copy.source.at, backupRunId: copy.source.backupRunId })
}

/** The copy's line under its name: what it is of, and how long it has. */
export function copyLine(copy: DataCopy, now: number) {
  const parts = [`as of release #${copy.releaseNumber}`, copySourceWords(copy)]
  if (copy.state === "ready" && copy.expiresAt) {
    parts.push(`removed in ${remaining(copy.expiresAt, now)}`)
  } else if (copy.reason && !copyActive(copy.state)) {
    parts.push(copy.reason)
  }
  return parts.join(" · ")
}

/** The copies a run's page links: the ones that waited for that run. */
export function copiesOfRun(copies: DataCopy[] | undefined, runId: number) {
  return (copies ?? []).filter((copy) => copy.afterRunId === runId)
}

/** Active copies first, each group newest first: what is the operator's now leads. */
export function orderCopies(copies: DataCopy[] | undefined) {
  return [...(copies ?? [])].sort((a, b) => {
    const live = Number(copyActive(b.state)) - Number(copyActive(a.state))
    return live || Date.parse(b.createdAt) - Date.parse(a.createdAt)
  })
}

type ComparedTable = DataCopyComparison["tables"][number]

/** Whether a table reads differently in the copy than live. */
export function tableDiffers(table: ComparedTable) {
  if (table.only) return true
  if (table.copyRows === null || table.liveRows === null) return false
  return table.copyRows !== table.liveRows
}

/** "+12", "−3", "only in the copy": a table's difference as the reader scans for it. */
export function tableDifference(table: ComparedTable) {
  if (table.only === "copy") return "only in the copy"
  if (table.only === "live") return "only live"
  if (table.copyRows === null || table.liveRows === null) return "not counted"
  const delta = table.liveRows - table.copyRows
  if (delta === 0) return "same"
  return delta > 0 ? `+${delta.toLocaleString()} live` : `−${(-delta).toLocaleString()} live`
}

/** The comparison's one-line reading: how many tables differ. */
export function comparisonSummary(comparison: DataCopyComparison) {
  const differ = comparison.tables.filter(tableDiffers).length
  const total = comparison.tables.length
  if (total === 0) return "No tables to compare"
  if (differ === 0) return `All ${plural(total, "table")} match`
  return `${differ} of ${plural(total, "table")} differ`
}

/**
 * The "What changed" sheet's Data point section: each linked database's point
 * in one release beside the other, as the other sections read — a database
 * linked in one release and not the other is added or removed.
 */
export function dataPointChanges(
  from: ReleaseDataPoint[] | undefined,
  to: ReleaseDataPoint[] | undefined,
): ReleaseListChange[] {
  const before = new Map((from ?? []).map((point) => [point.connectionId, point]))
  const after = new Map((to ?? []).map((point) => [point.connectionId, point]))
  const ids = [...new Set([...before.keys(), ...after.keys()])].sort((a, b) => a - b)
  return ids.map((id) => {
    const old = before.get(id)
    const next = after.get(id)
    const name = (next ?? old)?.name ?? `connection ${id}`
    const fromWords = old ? dataPointWords(old) : undefined
    const toWords = next ? dataPointWords(next) : undefined
    const change: ReleaseListChange["change"] = !old
      ? "added"
      : !next
        ? "removed"
        : fromWords === toWords
          ? "unchanged"
          : "changed"
    return { name, change, from: fromWords, to: toWords }
  })
}
