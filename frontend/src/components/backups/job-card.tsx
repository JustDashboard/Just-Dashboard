"use client"

import { useCallback } from "react"
import { Archive, CloudUpload, FolderClosed } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { BackupJob, BackupRun } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import { ChoiceRow } from "@/components/flow"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  contentsLabel,
  retentionLabel,
  scheduleLabel,
  targetLabel,
} from "@/components/backups/shared"
import { destinationProduct } from "@/components/backups/marks"

/** How many runs a card draws. Two weeks of a daily job, a day of an hourly one. */
const STRIP = 14

/**
 * One backup job, as the thing it is: what it protects, drawn as those
 * products; how its last run went, in the colour of the outcome; where it
 * writes, as the service it writes to; and its last runs as a strip, so a job
 * that has been failing every other night reads as that before its name is
 * read.
 *
 * A card rather than a table row because every one of these opens the job's
 * own page — a list of destinations (§12, §16) — and because the four
 * readings the page used to open on (jobs, last backup, next backup, stored)
 * are this job's and belong on it: the last run beside the name, the next one
 * and the storage under it.
 */
export function JobCard({
  job,
  products,
  verbs,
}: {
  job: BackupJob
  /** The products of what the job covers, from the coverage report. */
  products: string[]
  verbs: Verb[]
}) {
  const fetchRuns = useCallback(
    (signal: AbortSignal) =>
      get<{ runs: BackupRun[]; running: boolean }>(
        `/backups/${job.id}/runs`,
        { limit: STRIP },
        signal,
      ),
    [job.id],
  )
  const runs = usePoll(fetchRuns, 60_000, [job.id])
  // On a phone the outcome goes under the name, which otherwise had the
  // width the outcome and the verbs left it — none. Chosen once rather than
  // drawn twice and hidden, so the reading is in the document once.
  const wide = useMediaQuery("(min-width: 640px)")

  return (
    <ChoiceRow
      verb={job.name}
      href={`/backups/${job.id}`}
      className={cn(!job.enabled && job.schedule && "opacity-80")}
      leading={
        products.length > 1 ? (
          <ProductLogos ids={products} />
        ) : (
          <ProductLogo id={products[0]} size="sm" fallback={Archive} />
        )
      }
      title={job.name}
      description={contentsLabel(job)}
      trailing={wide ? <LastRun job={job} /> : undefined}
      actions={<VerbActions dim verbs={verbs} menuLabel={`More actions for ${job.name}`} />}
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2 text-hint text-muted-foreground sm:pl-11">
        {!wide && <LastRun job={job} />}
        <span className="flex min-w-0 items-center gap-1.5">
          {destinationProduct(job) ? (
            <ProductLogo
              id={destinationProduct(job)}
              size="sm"
              className="size-5 rounded-sm [&_img]:size-3.5"
            />
          ) : job.targetKind === "local" ? (
            <FolderClosed aria-hidden className="size-3.5 shrink-0" />
          ) : (
            <CloudUpload aria-hidden className="size-3.5 shrink-0" />
          )}
          <span className={cn("truncate", job.targetKind === "local" && "font-mono")}>
            {targetLabel(job)}
          </span>
        </span>
        <RunStrip runs={runs.data?.runs} />
        <span className={cn("whitespace-nowrap", job.overdue && "font-medium text-warning")}>
          {nextLabel(job)}
        </span>
        <span className="numeric whitespace-nowrap">
          {job.stored.runs > 0
            ? `${bytes(job.stored.bytes)} in ${plural(job.stored.runs, "archive")}`
            : "nothing stored yet"}
          <span className="text-muted-foreground/60"> · {retentionLabel(job).toLowerCase()}</span>
        </span>
      </div>
    </ChoiceRow>
  )
}

/** The last run's outcome, in its colour, beside the name. */
function LastRun({ job }: { job: BackupJob }) {
  if (!job.enabled && job.schedule) return <Status state="stopped" label="paused" />
  if (!job.lastRun) return <span className="text-xs text-muted-foreground">never run</span>
  const { status, startedAt } = job.lastRun
  return (
    <Status
      state={status}
      live={status === "running"}
      label={
        status === "running"
          ? "running now"
          : `${status === "success" ? "backed up" : "failed"} ${relativeTime(startedAt)}`
      }
    />
  )
}

/**
 * The job's recent runs, oldest first: a square per run in the colour of how
 * it ended. Squares with a radius of their own, not dots — a row of filled
 * circles reads as a row of pills (§4) — and no tooltip machinery for
 * fourteen marks: each says what it was in its title.
 */
function RunStrip({ runs }: { runs: BackupRun[] | undefined }) {
  if (!runs || runs.length === 0) return null
  const ordered = [...runs].reverse()
  return (
    <span
      role="img"
      aria-label={`Last ${plural(ordered.length, "run")}: ${ordered.filter((r) => r.status === "failed").length} failed`}
      className="flex shrink-0 items-center gap-0.5"
    >
      {ordered.map((run) => (
        <span
          key={run.id}
          title={`${run.status} · ${relativeTime(run.startedAt)}${run.sizeBytes ? ` · ${bytes(run.sizeBytes)}` : ""}`}
          className={cn(
            "h-3 w-1.5 rounded-sm",
            run.status === "success" && "bg-success/80",
            run.status === "failed" && "bg-destructive",
            run.status === "running" && "bg-brand",
          )}
        />
      ))}
    </span>
  )
}

export function nextLabel(job: BackupJob): string {
  if (!job.schedule) return "runs by hand"
  if (!job.enabled) return `paused · ${scheduleLabel(job.schedule).toLowerCase()}`
  if (job.overdue) return `overdue · next ${job.nextRun ? relativeTime(job.nextRun) : "unknown"}`
  const next = job.nextRun ? `next ${relativeTime(job.nextRun)}` : "not scheduled"
  return `${next} · ${scheduleLabel(job.schedule).toLowerCase()}`
}
