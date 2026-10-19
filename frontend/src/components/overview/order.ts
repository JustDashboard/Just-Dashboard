import type { BoardSummary } from "@/lib/boards"
import type { StickySummary } from "@/lib/stickyboard"
import type { BackupJob, SystemdUnit, TerminalWorkspace } from "@/lib/types"

/**
 * What leads each of the Overview's lists, kept apart from the drawing so it
 * can be tested in a millisecond. Every list on the page is capped, so the
 * order is the whole of what decides which rows a reader ever sees there.
 */

/** A drawing or a sticky board, as one list of the things last worked on. */
export type RecentWork =
  | {
      kind: "drawing"
      key: string
      name: string
      updatedAt: string
      href: string
      drawing: BoardSummary
    }
  | {
      kind: "board"
      key: string
      name: string
      updatedAt: string
      href: string
      board: StickySummary
    }

/**
 * The drawings and boards edited most recently, newest first, as one list:
 * which tool a thing was made in is a property of it, not a reason to look in
 * two places for what was open yesterday.
 */
export function recentWork(
  drawings: readonly BoardSummary[] | undefined,
  boards: readonly StickySummary[] | undefined,
  limit: number,
): RecentWork[] {
  const work: RecentWork[] = [
    ...(drawings ?? []).map((drawing) => ({
      kind: "drawing" as const,
      key: `drawing:${drawing.id}`,
      name: drawing.name,
      updatedAt: drawing.updatedAt,
      href: `/excalidraw/${drawing.id}`,
      drawing,
    })),
    ...(boards ?? []).map((board) => ({
      kind: "board" as const,
      key: `board:${board.id}`,
      name: board.name,
      updatedAt: board.updatedAt,
      href: `/stickyboard/${board.id}`,
      board,
    })),
  ]
  return work
    .sort(
      (a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt) || a.name.localeCompare(b.name),
    )
    .slice(0, limit)
}

/**
 * The shells worth going back to first: one working before one holding a
 * program at its prompt, before one sitting at a prompt, before one whose
 * process has ended; a favourite before the rest of its kind, then the newest.
 */
export function shellOrder(sessions: readonly TerminalWorkspace[]): TerminalWorkspace[] {
  const rank = (session: TerminalWorkspace) =>
    !session.live ? 3 : session.working ? 0 : session.busy ? 1 : 2
  return [...sessions].sort(
    (a, b) =>
      rank(a) - rank(b) ||
      Number(b.favourite) - Number(a.favourite) ||
      Date.parse(b.createdAt) - Date.parse(a.createdAt),
  )
}

/**
 * Backup jobs in the order they need reading: a failed last run, then one
 * running now, then the scheduled ones by what runs next, the by-hand ones,
 * and the paused last — the Backups page's order, so the job leading the
 * Overview's rows is the job leading its cards.
 */
export function backupOrder(jobs: readonly BackupJob[]): BackupJob[] {
  const rank = (job: BackupJob) => {
    if (job.lastRun?.status === "failed") return 0
    if (job.lastRun?.status === "running") return 1
    if (!job.schedule) return 3
    return job.enabled ? 2 : 4
  }
  const next = (job: BackupJob) => (job.nextRun ? Date.parse(job.nextRun) : Infinity)
  return [...jobs].sort(
    (a, b) => rank(a) - rank(b) || next(a) - next(b) || a.name.localeCompare(b.name),
  )
}

/**
 * How many jobs' last run failed. Every job is counted rather than read off
 * the newest run on the server: a nightly job that failed was hidden behind
 * an hourly one that succeeded after it.
 */
export function failedBackups(jobs: readonly BackupJob[]): number {
  return jobs.filter((job) => job.lastRun?.status === "failed").length
}

/** The services systemd is running, and those it gave up on. */
export function unitCounts(units: readonly SystemdUnit[]): { running: number; failed: number } {
  const services = units.filter((unit) => unit.name.endsWith(".service"))
  return {
    running: services.filter((unit) => unit.activeState === "active").length,
    failed: units.filter((unit) => unit.activeState === "failed").length,
  }
}
