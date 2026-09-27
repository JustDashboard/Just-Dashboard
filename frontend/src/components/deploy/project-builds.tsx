"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowUpRight, Wrench } from "@/components/icons"
import { cn } from "@/lib/utils"
import { relativeTime, timestamp } from "@/lib/format"
import type { DeploymentEngineRun } from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { FactDot } from "@/components/metrics/host-identity"
import { EmptyState, LoadingRows } from "@/components/state"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { BuildConsole } from "@/components/deploy/build-console"
import { RunActorMark } from "@/components/deploy/run-marks"
import { useRunTranscript } from "@/components/deploy/run-stream"
import {
  RunStatus,
  formatDuration,
  isActiveRun,
  runDurationSeconds,
  runSubject,
  runTitle,
  useNow,
} from "@/components/deploy/vocabulary"

/**
 * What the builds said, beside what the running release says.
 *
 * The recent runs down a rail, each with how it ended, and the chosen one's
 * transcript beside them in the run page's own console — search, errors,
 * stages, copy and download — so "the deploy an hour ago failed, what did it
 * print" is a press on the page that shows the failures it caused, rather
 * than a trip to the run and back. The run's own page is one link away, for
 * everything a transcript is not: its steps, its release, its verbs.
 *
 * The rail is the logs page's source rail in shape (a neutral fill for the
 * chosen row, a hover wash, nothing lifted); on a phone there is no width for
 * it and the runs are the header's picker instead — chosen once by width,
 * so the list is in the document once.
 */
export function ProjectBuilds({
  projectId,
  runs,
  loading,
  remote,
}: {
  projectId: number
  /** Newest first. */
  runs: DeploymentEngineRun[]
  loading: boolean
  /** The source's remote, for a push's host on the run's mark. */
  remote?: string
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const [picked, setPicked] = useState<number>()
  const run = runs.find((candidate) => candidate.id === picked) ?? runs[0]

  if (!run) {
    return loading ? (
      <LoadingRows rows={6} className="p-3" />
    ) : (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyState
          icon={Wrench}
          title="Nothing has been built yet"
          description="A deployment's first run writes its transcript here — every command the build ran and what it printed."
        />
      </div>
    )
  }

  return (
    <div className="flex min-h-0 flex-1">
      {wide && (
        <nav
          aria-label="Builds"
          className="flex w-64 shrink-0 flex-col gap-px overflow-y-auto border-r border-hairline p-1.5"
        >
          {runs.map((candidate) => (
            <BuildRow
              key={candidate.id}
              run={candidate}
              remote={remote}
              selected={candidate.id === run.id}
              onSelect={() => setPicked(candidate.id)}
            />
          ))}
        </nav>
      )}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-hairline px-3 py-1">
          {wide ? (
            <span className="flex min-w-0 items-baseline gap-2">
              <span className="shrink-0 text-body font-medium">{runTitle(run)}</span>
              {runSubject(run) && (
                <span className="truncate text-hint text-muted-foreground">{runSubject(run)}</span>
              )}
            </span>
          ) : (
            <Select value={String(run.id)} onValueChange={(value) => setPicked(Number(value))}>
              <SelectTrigger
                size="sm"
                aria-label="Build"
                className="h-8 max-w-60 min-w-0 gap-1.5 border-transparent bg-transparent px-1.5 font-medium shadow-none"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {runs.map((candidate) => (
                  <SelectItem key={candidate.id} value={String(candidate.id)}>
                    {runTitle(candidate)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <Link
            href={`/deploy/${projectId}/runs/${run.id}`}
            className="ml-auto inline-flex shrink-0 items-center gap-1 rounded-sm text-hint font-medium text-foreground focus-ring hover:underline"
          >
            Open the run
            <ArrowUpRight aria-hidden className="size-3" />
          </Link>
        </div>
        <Transcript key={run.id} projectId={projectId} run={run} />
      </div>
    </div>
  )
}

/** One run in the rail: who started it, what it was, how it went, how long, when. */
function BuildRow({
  run,
  remote,
  selected,
  onSelect,
}: {
  run: DeploymentEngineRun
  remote?: string
  selected: boolean
  onSelect: () => void
}) {
  const active = isActiveRun(run.state)
  const now = useNow(1000, active)
  const subject = runSubject(run)
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={selected ? "true" : undefined}
      title={subject}
      className={cn(
        "flex w-full min-w-0 items-center gap-2.5 rounded-md px-2 py-1.5 text-left focus-ring-inset transition-colors",
        selected ? "bg-accent text-foreground" : "hover:bg-row-hover",
      )}
    >
      <RunActorMark run={run} remote={remote} />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex min-w-0 items-baseline gap-2">
          <span className={cn("shrink-0 text-body leading-tight", selected && "font-medium")}>
            {runTitle(run)}
          </span>
          {subject && <span className="truncate text-hint text-muted-foreground">{subject}</span>}
        </span>
        <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
          <RunStatus state={run.state} live={active} className="shrink-0" />
          <FactDot />
          <span className="numeric shrink-0">{formatDuration(runDurationSeconds(run, now))}</span>
          <FactDot />
          <time
            dateTime={run.requestedAt}
            title={timestamp(run.requestedAt)}
            className="numeric truncate"
          >
            {relativeTime(run.requestedAt)}
          </time>
        </span>
      </span>
    </button>
  )
}

/** One run's transcript, read from its stream while it is on screen. */
function Transcript({ projectId, run }: { projectId: number; run: DeploymentEngineRun }) {
  const transcript = useRunTranscript(projectId, run.id)
  const [step, setStep] = useState<number>()
  // The stream's snapshot is the run as the transcript knows it; until it
  // lands, the list's copy says how the run stands.
  const current = transcript.snapshot?.run ?? run
  const active = isActiveRun(current.state)
  const now = useNow(1000, active)
  return (
    <BuildConsole
      flush
      className="min-h-0 flex-1"
      rows={transcript.rows}
      summary={transcript.summary}
      capped={transcript.capped}
      steps={transcript.steps}
      active={active}
      outcome={current.state}
      connected={transcript.connected}
      startedAt={current.claimedAt ?? current.requestedAt}
      runNumber={current.runNumber}
      now={now}
      selectedStep={step}
      onSelectStep={setStep}
    />
  )
}
