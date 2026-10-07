"use client"

import { useMemo, useState } from "react"
import { ChevronDown, RefreshClockwise, Stopwatch, Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import { get } from "@/lib/api"
import { bytes, clock, duration, plural, relativeTime, timestamp } from "@/lib/format"
import { journalSource } from "@/lib/log-sources"
import { latency } from "@/lib/requests"
import type { LogSearchResult } from "@/lib/types"
import {
  MANAGER,
  RUN_EVENTS,
  RUNS_DAYS,
  crashLoop,
  foldRuns,
  managerLines,
  runLength,
  runWindow,
  unitRuns,
  type CrashLoop,
  type RunOutcome,
  type UnitRun,
} from "@/lib/unit-runs"
import { usePoll } from "@/hooks/use-poll"
import { useNow } from "@/components/deploy/vocabulary"
import { IconAction } from "@/components/icon-action"
import type { ServiceLogsContext, ServiceLogsView } from "@/components/logs/service-logs"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { RESULT_WORDS } from "@/components/procs/units"

/**
 * The most lifecycle lines one read returns. A unit restarting every five
 * seconds writes about six a run, so this is the last few hundred runs of a
 * loop and every run of anything calmer.
 */
const RUNS_LIMIT = 2000

/** How many of a folded row's runs it lists when opened. */
const FOLD_SHOWN = 50

/**
 * How often the runs are read again on their own. A week of a crash-looping
 * unit's journal takes journalctl tens of seconds to read, so a view left
 * open would keep one running on the host it is watching; a run that ends
 * meanwhile is one press of the refresh away.
 */
const RUNS_POLL = 5 * 60_000

/** The query: the lifecycle events, from the manager only. */
const RUN_PREDICATES = [...RUN_EVENTS.map((event) => `event:${event}`), `program:${MANAGER}`]

const OUTCOME: Record<RunOutcome, { label: string; tone: DotTone }> = {
  running: { label: "running", tone: "running" },
  succeeded: { label: "succeeded", tone: "running" },
  failed: { label: "failed", tone: "danger" },
  killed: { label: "killed", tone: "warning" },
  restarted: { label: "restarted", tone: "warning" },
  stopped: { label: "stopped", tone: "stopped" },
  ended: { label: "ended", tone: "unknown" },
}

/**
 * The Runs view, as every page that reads a unit's journal offers it — the
 * unit's sheet, a timer's row and `/logs` — so the view is one definition
 * rather than one per page.
 */
export function unitRunsView(unit: string): ServiceLogsView {
  return { id: "runs", label: "Runs", render: (ctx) => <UnitRuns unit={unit} ctx={ctx} /> }
}

/**
 * A unit's runs over the last week: each time systemd started it, how long
 * it lasted and how it ended, newest first.
 *
 * Read in one search of the unit's journal through the systemd lens, asking
 * only for the manager's lifecycle lines — and only from the manager, since
 * the forced lens would name the program's own "Started worker pool" a start
 * as well — and grouped here by invocation (`lib/unit-runs.ts`). The tail of the window rather than its
 * head, because a unit in a crash loop writes a week of lines in an
 * afternoon and the runs worth reading are the latest. A row opens the run's
 * own lines in History — the program's and the manager's, narrowed to its
 * invocation — which is where the reason it exited is.
 */
export function UnitRuns({ unit, ctx }: { unit: string; ctx: ServiceLogsContext }) {
  const read = usePoll(
    async (signal) => {
      const since = new Date(Date.now() - RUNS_DAYS * 86_400_000).toISOString()
      const result = await get<LogSearchResult>(
        "/logs/search",
        {
          source: journalSource(unit),
          lens: "systemd",
          f: RUN_PREDICATES,
          since,
          limit: RUNS_LIMIT,
        },
        signal,
      )
      return { since, result, at: Date.now() }
    },
    RUNS_POLL,
    [unit],
  )
  const result = read.data?.result
  const lines = useMemo(() => managerLines(result?.lines ?? [], unit), [result, unit])
  const runs = useMemo(() => unitRuns(lines), [lines])
  const groups = useMemo(() => foldRuns(runs), [runs])
  const loop = useMemo(() => crashLoop(lines), [lines])
  const failed = runs.filter((run) => run.outcome === "failed").length
  // A running run's length is read against a clock that moves, and that
  // is never behind the read that found it running.
  const tick = useNow(60_000, runs.at(-1)?.outcome === "running")
  const now = Math.max(tick, read.data?.at ?? 0)
  // The refresh is pending until the read it asked for has answered, which
  // replaces the result or the error it was pressed over.
  const [asked, setAsked] = useState<{ data: unknown; error: unknown }>()
  const pending = asked !== undefined && asked.data === read.data && asked.error === read.error
  const again = () => {
    setAsked({ data: read.data, error: read.error })
    read.refresh()
  }
  // Short of the whole week, the count says what it is of: the runs since
  // the oldest line read. A capped read keeps the newest lines, and so does
  // one that ran out of time, since the journal is read newest first.
  const oldest = runs[0]?.start ?? runs[0]?.first
  const count =
    result && (result.truncated || !result.complete) && oldest
      ? `${plural(runs.length, "run")} since ${when(oldest)}`
      : `${plural(runs.length, "run")} in ${RUNS_DAYS} days`

  const open = (run: UnitRun) => {
    const window = runWindow(run, Date.now(), read.data?.since)
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
      <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-hairline py-1 pr-1.5 pl-3 text-hint">
        {result ? (
          <span className="numeric min-w-0 truncate text-muted-foreground">
            {count}
            {failed > 0 && <span className="font-medium text-destructive"> · {failed} failed</span>}
          </span>
        ) : (
          <span className="text-muted-foreground">Reading the journal…</span>
        )}
        <IconAction
          label="Read the runs again"
          className="ml-auto size-7"
          pending={pending}
          disabled={!read.data && !read.error}
          onClick={again}
        >
          <RefreshClockwise />
        </IconAction>
      </div>

      {loop && (
        <div className="shrink-0 border-b border-hairline p-3">
          <LoopNotice unit={unit} loop={loop} newest={runs.at(-1)} />
        </div>
      )}

      {!result ? (
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
          {/* Keyed on a group's oldest run: a loop that goes on adds newer
              ones at its head, and the fold the reader opened stays open. */}
          {groups.map((group) => (
            <RunGroup
              key={`${group.at(-1)!.invocation}|${group.at(-1)!.first}`}
              runs={group}
              now={now}
              onOpen={open}
            />
          ))}
        </ul>
      )}

      {result && !result.complete ? (
        // The journal is read newest first, so a read cut short by its time
        // limit is missing the week's oldest runs, not the latest.
        <p className="border-t border-hairline px-3 py-2 text-hint text-warning">
          The journal took longer to read than a search is allowed, so the week&apos;s oldest runs
          are missing. History reads a shorter stretch in full.
        </p>
      ) : (
        result?.truncated && (
          <p className="border-t border-hairline px-3 py-2 text-hint text-muted-foreground">
            The latest {result.lines.length.toLocaleString()} of {result.matched.toLocaleString()}{" "}
            lifecycle lines; the runs before them are in History.
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
function RunGroup({
  runs,
  now,
  onOpen,
}: {
  runs: UnitRun[]
  now: number
  onOpen: (run: UnitRun) => void
}) {
  const [open, setOpen] = useState(false)
  const [head] = runs
  const oldest = runs[runs.length - 1]
  const folded = runs.length > 1
  return (
    <li className="min-w-0">
      <div className="flex min-w-0 items-stretch transition-colors hover:bg-row-hover">
        <RunLine
          run={head}
          now={now}
          onOpen={onOpen}
          since={folded ? (oldest.start ?? oldest.first) : undefined}
          length={!folded}
        />
        {folded && (
          <div className="flex shrink-0 flex-col items-end gap-0.5 py-2 pr-3">
            <RunLength run={head} now={now} />
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
              key={`${run.invocation}|${run.first}`}
              className="flex min-w-0 pl-4 transition-colors hover:bg-row-hover"
            >
              <RunLine run={run} now={now} onOpen={onOpen} length />
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
 * whole of it is the press that opens its lines. A run whose start is older
 * than the lines read says when it stopped instead, and has no length.
 */
function RunLine({
  run,
  now,
  onOpen,
  since,
  length,
}: {
  run: UnitRun
  now: number
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
    run.cpu !== undefined && `${span(run.cpu)} CPU`,
    run.memory !== undefined && `${bytes(run.memory)} peak`,
    since && `since ${when(since)}`,
  ].filter(Boolean)
  return (
    <button
      type="button"
      onClick={() => onOpen(run)}
      disabled={!run.first}
      title={run.first ? "Open this run's lines in History" : undefined}
      className={cn(
        "grid min-w-0 flex-1 items-center gap-x-3 gap-y-0.5 py-2 pl-3 text-left focus-ring-inset",
        length ? "grid-cols-[5rem_minmax(0,1fr)_auto] pr-3" : "grid-cols-[5rem_minmax(0,1fr)]",
      )}
    >
      <Status tone={outcome.tone} label={outcome.label} />
      {run.start ? (
        <span className="numeric min-w-0 truncate text-body" title={timestamp(run.start)}>
          {when(run.start)}
        </span>
      ) : (
        <span
          className="numeric min-w-0 truncate text-body text-muted-foreground"
          title="It started before the lines read here"
        >
          {run.first ? `until ${when(run.end ?? run.last ?? run.first)}` : "—"}
        </span>
      )}
      {length && <RunLength run={run} now={now} />}
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

function RunLength({ run, now }: { run: UnitRun; now: number }) {
  const ms = runLength(run, now)
  return (
    <span className="numeric text-right text-body text-muted-foreground">
      {ms === undefined ? "—" : span(ms)}
    </span>
  )
}

/** A length in milliseconds: to the hundredth under a minute, in units past it. */
function span(ms: number) {
  return ms < 60_000 ? latency(ms) : duration(ms / 1000)
}

/** Today's runs by the clock; older ones with their day, since a week is the window. */
function when(iso: string) {
  const d = new Date(iso)
  if (d.toDateString() === new Date().toDateString()) return clock(iso)
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
  return RESULT_WORDS[result] ?? result.replace(/-/g, " ")
}
