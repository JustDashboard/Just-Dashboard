import { bytes, plural, relativeTime } from "@/lib/format"
import type { BackupJob } from "@/lib/types"
import { retentionLabel } from "@/components/backups/shared"

type Tone = "running" | "danger" | "warning" | "stopped"

/** One state beside the page's name: a dot's tone and its words. */
export type BackupState = { key: string; tone: Tone; label: string; live?: boolean }

/** A job this server runs. An imported one is another server's record: it never runs here. */
const own = (job: BackupJob) => !job.imported

/**
 * How the backups stand, as the states beside the page's name, worst first: a
 * job whose last run failed, one being taken now, and then either that every
 * job's last run landed or what stands in the way of saying so. A paused job is
 * said after them, because a paused schedule is a decision, not a fault.
 */
export function backupStates(jobs: BackupJob[]): BackupState[] {
  const mine = jobs.filter(own)
  const failed = mine.filter((job) => job.lastRun?.status === "failed").length
  const running = mine.filter((job) => job.lastRun?.status === "running").length
  const paused = mine.filter((job) => !job.enabled && job.schedule).length
  const landed = mine.filter((job) => job.lastRun?.status === "success").length
  const states: BackupState[] = []
  if (failed > 0) {
    states.push({ key: "failed", tone: "danger", label: `${plural(failed, "job")} failed` })
  }
  if (running > 0) {
    states.push({ key: "running", tone: "running", label: "Backing up now", live: true })
  }
  if (failed === 0 && running === 0) {
    if (jobs.length === 0) states.push({ key: "none", tone: "stopped", label: "No jobs" })
    else if (mine.length === 0)
      states.push({ key: "imported", tone: "stopped", label: "Imported only" })
    else if (landed === 0)
      states.push({ key: "never", tone: "stopped", label: "Nothing backed up yet" })
    else if (landed < mine.length)
      states.push({
        key: "partly",
        tone: "warning",
        label: `${plural(mine.length - landed, "job")} never run`,
      })
    else states.push({ key: "landed", tone: "running", label: "Every job backed up" })
  }
  if (paused > 0) states.push({ key: "paused", tone: "stopped", label: `${paused} paused` })
  return states
}

/** The jobs, what they hold and when the next one runs: the page's line of facts. */
export function backupFacts(jobs: BackupJob[]) {
  const stored = jobs.reduce((sum, job) => sum + job.stored.bytes, 0)
  const archives = jobs.reduce((sum, job) => sum + job.stored.runs, 0)
  const next = jobs
    .filter((job) => own(job) && job.enabled && job.nextRun)
    .map((job) => job.nextRun!)
    .sort()[0]
  return {
    jobs: plural(jobs.length, "job"),
    stored:
      archives > 0 ? `${bytes(stored)} in ${plural(archives, "archive")}` : "nothing stored yet",
    next: next
      ? `next backup ${relativeTime(next)}`
      : jobs.some(own)
        ? "nothing scheduled"
        : undefined,
  }
}

/**
 * What a job holds, as one reading: the archives and what they add up to, over
 * how many are kept and the size of the last one. An imported job's archives
 * are another server's to prune, so it says that where the rule would be.
 */
export function holdingReading(job: BackupJob, lastArchive: number | undefined) {
  const kept = job.imported ? "Never pruned from here" : retentionLabel(job)
  return {
    words:
      job.stored.runs > 0
        ? `${bytes(job.stored.bytes)} in ${plural(job.stored.runs, "archive")}`
        : "Nothing stored yet",
    hint: lastArchive !== undefined ? `${kept} · last archive ${bytes(lastArchive)}` : kept,
  }
}
