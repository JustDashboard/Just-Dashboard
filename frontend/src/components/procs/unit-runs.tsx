"use client"

import { useMemo, useState } from "react"
import { ChevronDown, Stopwatch, Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import { get } from "@/lib/api"
import { bytes, duration, plural, relativeTime, timestamp } from "@/lib/format"
import { journalSource } from "@/lib/log-sources"
import { latency } from "@/lib/requests"
import type { LogSearchResult } from "@/lib/types"
import {
  RUN_EVENTS,
  RUNS_DAYS,
  crashLoop,
  foldRuns,
  runLength,
  runWindow,
  unitRuns,
  type CrashLoop,
  type RunOutcome,
  type UnitRun,
} from "@/lib/unit-runs"
import { usePoll } from "@/hooks/use-poll"
import type { ServiceLogsContext } from "@/components/logs/service-logs"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"

/**
 * The most lifecycle lines one read returns. A unit restarting every five
 * seconds writes about six a run, so this is the last few hundred runs of a
 * loop and every run of anything calmer.
 */
const RUNS_LIMIT = 2000

/** How many of a folded row's runs it lists when opened. */
const FOLD_SHOWN = 50

const OUTCOME: Record<RunOutcome, { label: string; tone: DotTone }> = {
  running: { label: "running", tone: "running" },
  succeeded: { label: "succeeded", tone: "running" },
  failed: { label: "failed", tone: "danger" },
  killed: { label: "killed", tone: "warning" },
  restarted: { label: "restarted", tone: "warning" },
  stopped: { label: "stopped", tone: "stopped" },
  ended: { label: "ended", tone: "unknown" },
}

/** systemd's result words, where they say more than the exit beside them. */
const RESULT: Record<string, string> = {
  timeout: "timed out",
  "oom-kill": "killed for memory",
  "start-limit-hit": "start limit hit",
  watchdog: "watchdog timeout",
  "core-dump": "core dumped",
  resources: "resources unavailable",
  protocol: "protocol violation",
  "exec-condition": "condition failed",
}

/**
 * A unit's runs over the last week: each time systemd started it, how long
 * it lasted and how it ended, newest first.
 *
 * Read in one search of the unit's journal through the systemd lens, asking
 * only for the manager's lifecycle lines, and grouped here by invocation
 * (`lib/unit-runs.ts`). The tail of the window rather than its head, because
 * a unit in a crash loop writes a week of lines in an afternoon and the runs
 * worth reading are the latest. A row opens the run's own lines in History —
 * the program's and the manager's, narrowed to its invocation — which is
 * where the reason it exited is.
 */
export function UnitRuns({ unit, ctx }: { unit: string; ctx: ServiceLogsContext }) {
  const read = usePoll(
    (signal) =>
      get<LogSearchResult>(
        "/logs/search",
        {
          source: journalSource(unit),
          lens: "systemd",
          f: RUN_EVENTS.map((event) => `event:${event}`),
          since: new Date(Date.now() - RUNS_DAYS * 86_400_000).toISOString(),
          limit: RUNS_LIMIT,
        },
        signal,
      ),
    60_000,
    [unit],
  )
  const lines = read.data?.lines
  const runs = useMemo(() => unitRuns(lines ?? []), [lines])
  const groups = useMemo(() => foldRuns(runs), [runs])
  const loop = useMemo(() => crashLoop(lines ?? []), [lines])
  const failed = runs.filter((run) => run.outcome === "failed").length

  const open = (run: UnitRun) => {
    const window = runWindow(run)
    if (window) ctx.openHistory({ ...window, levels: [], q: "" })
  }

  if (read.error && !read.data) {
    return (
      <div className="flex flex-1 items-center justify-center p-6">
        <ErrorState error={read.error} className="max-w-lg" />
      </div>
    )
  }

  return (
    <div className="@container flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-hairline px-3 py-1 text-hint">
        {read.data ? (
          <span className="numeric min-w-0 truncate text-muted-foreground">
            {plural(runs.length, "run")} in {RUNS_DAYS} days
            {failed > 0 && <span className="font-medium text-destructive"> · {failed} failed</span>}
          </span>
        ) : (
          <span className="text-muted-foreground">Reading the journal…</span>
        )}
      </div>

      {loop && (
        <div className="shrink-0 border-b border-hairline p-3">
          <LoopNotice unit={unit} loop={loop} newest={runs.at(-1)} />
        </div>
      )}

      {!read.data ? (
        <LoadingRows rows={5} className="p-3" />
      ) : groups.length === 0 ? (
        <div className="flex flex-1 items-center justify-center p-6">
          <EmptyState
            icon={Stopwatch}
            title={`No runs in the last ${RUNS_DAYS} days`}
            description={
              ctx.source.status === "active"
                ? `${unit} has been up since before the window: systemd recorded no start or stop of it in the last ${RUNS_DAYS} days.`
                : `systemd recorded no start or stop of ${unit} in the last ${RUNS_DAYS} days — a unit that has not run, or a journal that keeps less than a week.`
            }
          />
        </div>
      ) : (
        <ul aria-label="Runs" className="divide-y divide-hairline">
          {groups.map((group) => (
            <RunGroup key={`${group[0].invocation}|${group[0].start}`} runs={group} onOpen={open} />
          ))}
        </ul>
      )}

      {read.data && !read.data.complete ? (
        // The journal is read oldest first, so a read cut short by its time
        // limit is missing the newest runs, not the oldest.
        <p className="border-t border-hairline px-3 py-2 text-hint text-warning">
          The journal took longer to read than a search is allowed, so the newest runs may be
          missing. History over the last hour reads it faster.
        </p>
      ) : (
        read.data?.truncated && (
          <p className="border-t border-hairline px-3 py-2 text-hint text-muted-foreground">
            The latest {read.data.lines.length.toLocaleString()} of{" "}
            {read.data.matched.toLocaleString()} lifecycle lines; the runs before them are in
            History.
          </p>
        )
      )}
    </div>
  )
}

/**
 * The latest burst of restarts, said once above the runs. It is still going
 * while the last restart is recent, unless systemd has since stopped trying:
 * a start limit hit is a unit left failed, which is the same fire with the
 * alarm switched off, and says so.
 */
function LoopNotice({
  unit,
  loop,
  newest,
}: {
  unit: string
  loop: CrashLoop
  newest: UnitRun | undefined
}) {
  const gaveUp = newest?.result === "start-limit-hit"
  const counter =
    loop.counter !== undefined
      ? ` (systemd's restart counter is at ${loop.counter.toLocaleString()})`
      : ""
  return (
    <Notice
      tone={gaveUp || loop.ongoing ? "danger" : "default"}
      icon={Warning}
      title={
        gaveUp
          ? `systemd stopped restarting ${unit}`
          : loop.ongoing
            ? `${unit} is restarting in a loop`
            : `${unit} was restarting in a loop ${relativeTime(loop.to)}`
      }
    >
      {plural(loop.count, "restart")} in {plural(minutesBetween(loop.from, loop.to), "minute")}
      {counter}
      {gaveUp
        ? ", then it hit its start limit and was left failed. The runs below say how each exited; open one for the lines it wrote."
        : ". Open a run below for the lines it wrote before it exited."}
    </Notice>
  )
}

/** At least one: three restarts inside the same minute are "in 1 minute", not "in 0". */
function minutesBetween(from: string, to: string) {
  return Math.max(1, Math.round((Date.parse(to) - Date.parse(from)) / 60_000))
}

/**
 * A row of one run, or of several that ended the same way, which open to
 * list each. A folded row keeps its length in the same column as every other
 * row's, with the count under it, so the lengths still read down the list.
 */
function RunGroup({ runs, onOpen }: { runs: UnitRun[]; onOpen: (run: UnitRun) => void }) {
  const [open, setOpen] = useState(false)
  const [head] = runs
  const oldest = runs[runs.length - 1]
  const folded = runs.length > 1
  return (
    <li className="min-w-0">
      <div className="flex min-w-0 items-stretch transition-colors hover:bg-row-hover">
        <RunLine
          run={head}
          onOpen={onOpen}
          since={folded ? oldest.start : undefined}
          length={!folded}
        />
        {folded && (
          <div className="flex shrink-0 flex-col items-end gap-0.5 py-2 pr-3">
            <RunLength run={head} />
            <button
              type="button"
              aria-expanded={open}
              aria-label={
                open
                  ? `Fold the ${runs.length} identical runs`
                  : `List the ${runs.length} identical runs`
              }
              onClick={() => setOpen(!open)}
              className="-mr-1 flex items-center gap-1 rounded-sm px-1 text-hint font-medium whitespace-nowrap text-muted-foreground focus-ring transition-colors hover:text-foreground"
            >
              <span className="numeric">×{runs.length.toLocaleString()}</span>
              <span className="@max-md:hidden">identical runs</span>
              <ChevronDown className={cn("size-3 transition-transform", open && "rotate-180")} />
            </button>
          </div>
        )}
      </div>
      {open && (
        <ul className="divide-y divide-hairline border-t border-hairline bg-surface-sunken">
          {runs.slice(0, FOLD_SHOWN).map((run) => (
            <li
              key={`${run.invocation}|${run.start}`}
              className="flex min-w-0 pl-4 transition-colors hover:bg-row-hover"
            >
              <RunLine run={run} onOpen={onOpen} length />
            </li>
          ))}
          {runs.length > FOLD_SHOWN && (
            <li className="py-2 pr-3 pl-7 text-hint text-muted-foreground">
              And {(runs.length - FOLD_SHOWN).toLocaleString()} earlier, the same.
            </li>
          )}
        </ul>
      )}
    </li>
  )
}

/**
 * One run: how it ended, when it started and for how long on the first
 * line; what systemd said of its exit and what it cost on the second. The
 * whole of it is the press that opens its lines.
 */
function RunLine({
  run,
  onOpen,
  since,
  length,
}: {
  run: UnitRun
  onOpen: (run: UnitRun) => void
  /** The oldest start of the identical runs this one heads. */
  since?: string
  /** Its length at the end of the first line; a folded row draws it beside the count. */
  length: boolean
}) {
  const outcome = OUTCOME[run.outcome]
  const facts = [
    exitWords(run),
    resultWords(run),
    run.restarted &&
      (run.restarts !== undefined ? `restart #${run.restarts.toLocaleString()}` : "restarted"),
    run.cpu !== undefined && `${latency(run.cpu)} CPU`,
    run.memory !== undefined && `${bytes(run.memory)} peak`,
    since && `since ${when(since)}`,
  ].filter(Boolean)
  return (
    <button
      type="button"
      onClick={() => onOpen(run)}
      disabled={!run.start}
      title={run.start ? "Open this run's lines in History" : undefined}
      className={cn(
        "grid min-w-0 flex-1 items-center gap-x-3 gap-y-0.5 py-2 pl-3 text-left focus-ring-inset",
        length ? "grid-cols-[5rem_minmax(0,1fr)_auto] pr-3" : "grid-cols-[5rem_minmax(0,1fr)]",
      )}
    >
      <Status tone={outcome.tone} label={outcome.label} />
      <span
        className="numeric min-w-0 truncate text-body"
        title={run.start ? timestamp(run.start) : undefined}
      >
        {run.start ? when(run.start) : "before the window"}
      </span>
      {length && <RunLength run={run} />}
      {facts.length > 0 && (
        <span
          className={cn(
            "col-start-2 truncate text-hint text-muted-foreground",
            length && "col-span-2",
          )}
        >
          {facts.join(" · ")}
        </span>
      )}
    </button>
  )
}

function RunLength({ run }: { run: UnitRun }) {
  const ms = runLength(run)
  return (
    <span className="numeric text-right text-body text-muted-foreground">
      {ms === undefined ? "—" : ms < 60_000 ? latency(ms) : duration(ms / 1000)}
    </span>
  )
}

/** Today's runs by the clock; older ones with their day, since a week is the window. */
function when(iso: string) {
  const d = new Date(iso)
  if (d.toDateString() === new Date().toDateString()) {
    return d.toLocaleTimeString(undefined, { hour12: false })
  }
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  })
}

function exitWords(run: UnitRun) {
  switch (run.exitCode) {
    case "exited":
      return run.exitStatus !== undefined ? `exit ${run.exitStatus}` : undefined
    case "killed":
      return run.signal ? `SIG${run.signal}` : `signal ${run.exitStatus ?? "?"}`
    case "dumped":
      return run.signal ? `core dumped, SIG${run.signal}` : "core dumped"
  }
  return undefined
}

function resultWords(run: UnitRun) {
  const result = run.result
  if (!result) return undefined
  if (result === "exit-code" && run.exitCode === "exited") return undefined
  if (result === "signal" && run.exitCode === "killed") return undefined
  if (result === "core-dump" && run.exitCode === "dumped") return undefined
  return RESULT[result] ?? result.replace(/-/g, " ")
}
