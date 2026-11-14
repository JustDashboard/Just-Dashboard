import { plural } from "@/lib/format"

/**
 * What the strip at the head of a database's settings says, worked out from
 * what the pages report. No React.
 */

/** One thing on the strip, in words: its name, what happened to it, where it is changed. */
export type PendingWord = {
  name: string
  /** A literal — a parameter, a library — set in mono. */
  mono?: boolean
  /** `added`, `changed` and `removed` take git's colours; any other word is a plain tag. */
  state: string
  href?: string
}

/** What one section puts on the strip. */
export type PendingReport = {
  /** Stored on the server, and read only when it next starts. */
  waiting?: PendingWord[]
  /**
   * The parameters `waiting` stands for. The page's own read of what waits
   * names a parameter; a section that says the same thing better (the
   * libraries behind `shared_preload_libraries`) takes its place.
   */
  covers?: string[]
  /** Held by a form and not saved. */
  edits?: PendingWord[]
  /** Reads the section's own state again, once a restart has ended. */
  refresh?: () => void
}

/**
 * Everything waiting and everything unsaved, each once: the parameters the
 * server marks, less the ones a section speaks for, then what the sections
 * hold, in the order they reported.
 */
export function pendingItems(
  stored: readonly PendingWord[],
  reports: readonly PendingReport[],
): { waiting: PendingWord[]; edits: PendingWord[] } {
  const covered = new Set(reports.flatMap((report) => report.covers ?? []))
  const waiting: PendingWord[] = []
  const seen = new Set<string>()
  const take = (item: PendingWord) => {
    if (seen.has(item.name)) return
    seen.add(item.name)
    waiting.push(item)
  }
  for (const report of reports) for (const item of report.waiting ?? []) take(item)
  for (const item of stored) if (!covered.has(item.name)) take(item)
  return { waiting, edits: reports.flatMap((report) => report.edits ?? []) }
}

/** The strip's headline: what waits, and what is typed beside it. */
export function pendingHeadline(waiting: number, edits: number): string {
  const stored = `${waiting} waiting for a restart`
  return edits > 0 ? `${stored} · ${plural(edits, "unsaved change")}` : stored
}
