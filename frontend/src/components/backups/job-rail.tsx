"use client"

import { useMemo, useState } from "react"
import { relativeTime, plural } from "@/lib/format"
import type { BackupJob, BackupResource, Container } from "@/lib/types"
import { StatusDot } from "@/components/status-dot"
import { Rail, RailLine, RailName, RailRow, railInk } from "@/components/rail"
import { JobMark, jobProducts, jobProject } from "@/components/backups/marks"

/**
 * The backup jobs, folded into a column beside the one opened (D9), as every
 * other host detail page has its list: each job as what it takes, its name,
 * and how its last run went and when, so moving to the next job is one press
 * rather than a trip back to Backups.
 */
export function JobRail({
  jobs,
  current,
  resources,
  containers,
  loading,
  error,
  onRetry,
}: {
  jobs: BackupJob[]
  current: number | null
  resources: BackupResource[]
  containers: Container[]
  loading: boolean
  error?: Error
  onRetry?: () => void
}) {
  const [query, setQuery] = useState("")
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    return q ? jobs.filter((job) => job.name.toLowerCase().includes(q)) : jobs
  }, [jobs, query])
  return (
    <Rail
      slot="backup-rail"
      label="Backup jobs"
      search={{ placeholder: "Job name", query, onQuery: setQuery }}
      rows={rows}
      keyOf={(job) => String(job.id)}
      current={(job) => job.id === current}
      row={(job, on) => (
        <JobRow job={job} current={on} resources={resources} containers={containers} />
      )}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty="No job matches."
      caption={
        loading && jobs.length === 0
          ? "Reading the jobs…"
          : query
            ? `${rows.length} of ${jobs.length}`
            : plural(jobs.length, "job")
      }
    />
  )
}

/** How a job's last run went, in the rail's few words. */
function lastLine(job: BackupJob): { text: string; tone?: "danger"; dot: string } {
  if (job.imported) return { text: "imported", dot: "stopped" }
  if (!job.enabled && job.schedule) return { text: "paused", dot: "stopped" }
  if (!job.lastRun) return { text: "never run", dot: "unknown" }
  const { status, startedAt } = job.lastRun
  if (status === "running") return { text: "running now", dot: "running" }
  if (status === "failed")
    return { text: `failed ${relativeTime(startedAt)}`, tone: "danger", dot: "failed" }
  return { text: `backed up ${relativeTime(startedAt)}`, dot: "running" }
}

function JobRow({
  job,
  current,
  resources,
  containers,
}: {
  job: BackupJob
  current: boolean
  resources: BackupResource[]
  containers: Container[]
}) {
  const line = lastLine(job)
  return (
    <RailRow
      item={String(job.id)}
      name={job.name}
      current={current}
      href={`/backups/${job.id}`}
      mark={
        <JobMark
          products={jobProducts(job, resources, containers)}
          project={jobProject(job, resources)}
        />
      }
      dot={<StatusDot state={line.dot} className="size-1.5" />}
    >
      <RailName current={current}>{job.name}</RailName>
      <RailLine className="numeric" ink={railInk(line.tone)}>
        {line.text}
      </RailLine>
    </RailRow>
  )
}
