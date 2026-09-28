import type { Job } from "@/lib/types"

/**
 * Each recent job's chip label. The target makes the better label when it is
 * a domain or a package set: it is short, and it is the thing that differs
 * between runs. A file path is neither — every SSH apply writes the same
 * drop-in, so a row of chips all read as the same truncated path and the
 * list stops being a way to find a run. Runs that still share a label — the
 * renewal service, run again after a fix — are told apart by when they
 * started: the minute, or the second where two share the minute.
 */
export function recentLabels(jobs: Pick<Job, "target" | "title" | "startedAt">[]): string[] {
  const base = jobs.map((job) =>
    job.target && !job.target.startsWith("/") ? job.target : job.title,
  )
  const withTime = (seconds: boolean) =>
    base.map((label, i) =>
      repeated(base, i) ? `${label} ${clock(jobs[i].startedAt, seconds)}` : label,
    )
  const minutes = withTime(false)
  const seconds = withTime(true)
  return minutes.map((label, i) => (repeated(minutes, i) ? seconds[i] : label))
}

function repeated(labels: string[], i: number): boolean {
  return labels.indexOf(labels[i]) !== labels.lastIndexOf(labels[i])
}

function clock(iso: string, seconds: boolean): string {
  const at = new Date(iso)
  const parts = [at.getHours(), at.getMinutes(), ...(seconds ? [at.getSeconds()] : [])]
  return parts.map((n) => String(n).padStart(2, "0")).join(":")
}
