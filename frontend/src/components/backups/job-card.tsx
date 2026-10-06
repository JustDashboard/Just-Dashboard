"use client"

import { useCallback, useLayoutEffect, useRef, useState } from "react"
import { Archive } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { BackupJob, BackupRun } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceRow } from "@/components/flow"
import { OutcomeStrip } from "@/components/outcome-strip"
import { ProductGlyph, ProductLogo, ProductLogos } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { VerbActions, type Verb } from "@/components/verbs"
import { contentsLabel, scheduleLabel, targetLabel } from "@/components/backups/shared"
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
 * are this job's and belong on it.
 *
 * One line where the card has room, so the verbs stand on its middle: what it
 * captures, when it next runs, where it writes and what it holds are one line
 * under the name, and the last run and the strip are its other end. They were
 * a band under the name, which made every job three lines tall with the verbs
 * level with its name over an empty corner. The strip keeps fourteen runs'
 * width however few it holds, so down a list the strips are one column and
 * the last runs end on one edge. How long archives are kept is the job's own
 * page's to say. A card without the room keeps a line under the name.
 */
export function JobCard({
  job,
  products,
  verbs,
  verb = job.name,
  note,
  working,
  index,
}: {
  job: BackupJob
  /** The products of what the job covers, from the coverage report. */
  products: string[]
  verbs: Verb[]
  /**
   * The card's accessible name where the page around it says more than the
   * job's name does — a deployment's settings open "the backup job" a release
   * gates on, not one job among many.
   */
  verb?: string
  /**
   * A reading the surface adds beside the last run — the live release's view
   * of the job — or under the name with it where the card is narrow.
   */
  note?: React.ReactNode
  /** The job is running now, so a light runs round the card (§11 *live*). */
  working?: boolean
  /** Position in the list, for the arrival stagger (§11 *arrived*). */
  index?: number
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
  const [mark, width] = useCardWidth()
  // The last run and the strip take some 280px of the card's end and the
  // verbs 60 more; a reading the surface adds, up to another 200. Short of
  // that the name would be left the width of a word, so they go under it.
  const wide = width >= (note ? 760 : 600)

  const contents = contentsLabel(job)
  const product = destinationProduct(job)
  const target = (
    <>
      {product && (
        <>
          <ProductGlyph id={product} className="inline-block align-[-2px]" />{" "}
        </>
      )}
      <span className={cn(job.targetKind === "local" && "font-mono")}>{targetLabel(job)}</span>
    </>
  )
  const next = (
    <span className={cn(job.overdue && "font-medium text-warning")}>{nextLabel(job)}</span>
  )
  const stored = (
    <span className="numeric">
      {job.stored.runs > 0
        ? `${bytes(job.stored.bytes)} in ${plural(job.stored.runs, "archive")}`
        : "nothing stored yet"}
    </span>
  )
  const last = <LastRun job={job} />
  const strip = <RunStrip runs={runs.data?.runs} />

  return (
    <ChoiceRow
      workspaceItem={{ id: String(job.id), name: job.name }}
      verb={verb}
      href={`/backups/${job.id}`}
      busy={working}
      index={index}
      className={cn(!job.enabled && job.schedule && "opacity-80")}
      leading={
        <span ref={mark} className="flex">
          {products.length > 1 ? (
            <ProductLogos ids={products} ring="ring-choice-surface" />
          ) : (
            <ProductLogo id={products[0]} size="sm" fallback={Archive} />
          )}
        </span>
      }
      title={job.name}
      description={
        // When it next runs before where it writes: an overdue job says so in
        // its colour, and the end of the line is what an ellipsis takes.
        wide ? (
          <>
            {contents && `${contents} · `}
            {next} · {target} · {stored}
          </>
        ) : (
          <>
            {contents && `${contents} · `}
            {target}
          </>
        )
      }
      trailing={
        wide && (
          <span className="flex items-center gap-4">
            {note}
            {last}
            <span className="flex w-27.5 justify-end">{strip}</span>
          </span>
        )
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`More actions for ${job.name}`} />}
    >
      {!wide && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2 text-hint text-muted-foreground sm:pl-11">
          {/* A line of their own, so the outcome is never read as one more
              word of the schedule after it. */}
          <div className="flex basis-full flex-wrap items-center gap-x-4 gap-y-1">
            {last}
            {note}
          </div>
          {strip}
          <span className="whitespace-nowrap">{next}</span>
          <span className="whitespace-nowrap">{stored}</span>
        </div>
      )}
    </ChoiceRow>
  )
}

/**
 * The card's own width. A job is drawn across /backups, in one half of a
 * project's runtime and in a settings page's 48rem column, so the window says
 * nothing about the room beside its name: at 1280 the runtime's half is about
 * 470px wide. The row is `ChoiceRow`'s list item, found from the mark inside
 * it, and measured before the first paint so a card never lands in one shape
 * and flips to the other.
 */
function useCardWidth() {
  const ref = useRef<HTMLSpanElement>(null)
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const card = ref.current?.closest("[data-slot=choice-row]")
    if (!card) return
    setWidth(card.getBoundingClientRect().width)
    const observer = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width))
    observer.observe(card)
    return () => observer.disconnect()
  }, [])
  return [ref, width] as const
}

/**
 * The last run's outcome, in its colour, beside the name. Exported for the
 * other surfaces that draw a backup job — a project's runtime, its database
 * settings — so the words and colours are this card's rather than a copy.
 */
export function LastRun({ job }: { job: BackupJob }) {
  if (!job.enabled && job.schedule) return <Status state="stopped" label="paused" />
  if (!job.lastRun) return <span className="text-xs text-muted-foreground">never run</span>
  const { status, startedAt } = job.lastRun
  return (
    <Status
      // A backup that did not land is a failure, not a caution: red, as its
      // square in the strip beside it and its line in the picture are.
      state={status}
      tone={status === "failed" ? "danger" : undefined}
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
 * The job's recent runs as the shared outcome strip, oldest first. The API
 * answers newest first, which is the order a list reads; a strip reads left to
 * right into the present.
 */
export function RunStrip({ runs }: { runs: BackupRun[] | undefined }) {
  if (!runs) return null
  const ordered = [...runs].reverse()
  return (
    <OutcomeStrip
      label={`Last ${plural(ordered.length, "run")}: ${ordered.filter((r) => r.status === "failed").length} failed`}
      items={ordered.map((run) => ({
        key: String(run.id),
        tone: run.status === "success" ? "success" : run.status === "failed" ? "danger" : "running",
        title: `${run.status} · ${relativeTime(run.startedAt)}${run.sizeBytes ? ` · ${bytes(run.sizeBytes)}` : ""}`,
      }))}
    />
  )
}

export function nextLabel(job: BackupJob): string {
  if (!job.schedule) return "runs by hand"
  if (!job.enabled) return `paused · ${scheduleLabel(job.schedule).toLowerCase()}`
  if (job.overdue) return `overdue · next ${job.nextRun ? relativeTime(job.nextRun) : "unknown"}`
  const next = job.nextRun ? `next ${relativeTime(job.nextRun)}` : "not scheduled"
  return `${next} · ${scheduleLabel(job.schedule).toLowerCase()}`
}
