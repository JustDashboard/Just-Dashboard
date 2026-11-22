"use client"

import { memo, useEffect, useMemo, useRef, useState } from "react"
import { Copy, Download, Logs } from "@/components/icons"
import { Pane, PaneFooter, PaneHeader } from "@/components/panel"
import { SearchInput } from "@/components/search-input"
import { ChipCount, ChipStrip, FilterChip } from "@/components/chips"
import { IconAction } from "@/components/icon-action"
import { Status } from "@/components/status-dot"
import { TranscriptRow, useTranscriptDrip } from "@/components/transcript-line"
import { BorderBeam } from "@/components/ui/border-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  RUN_LABELS,
  StepMark,
  runFailed,
  runTone,
  stepName,
  stepSeconds,
} from "@/components/deploy/vocabulary"
import { clock, duration, plural } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import { useNow } from "@/hooks/use-now"
import type { DeploymentRunState, DeploymentStep, DeploymentSummary } from "@/lib/types"
import { StepTile } from "@/components/deploy/run-evidence"
import { WireMark } from "@/components/wire/wire"
import { saveText } from "@/lib/download"
import { useBreakpoint } from "@/lib/breakpoints"
import { elapsed, stampColumnCh } from "./build-stamp"
import {
  consoleRows,
  type BuildLogEvent,
  type ConsoleRow,
  type TranscriptBuffer,
  type TranscriptCounts,
} from "./transcript-buffer"

export { consoleRows }
export type { BuildLogEvent, ConsoleRow }

/**
 * The transcript in three numbers, for the console's own counts and the
 * readings outside it — the view strip's count and its error mark, and
 * Details' lines per step. Read once from the parsed rows, by the page.
 */
export function transcriptSummary(rows: ConsoleRow[]): TranscriptCounts {
  const perStep = new Map<number, number>()
  let lines = 0
  let errors = 0
  for (const row of rows) {
    lines += 1
    if (row.line.kind === "error") errors += 1
    perStep.set(row.stepId, (perStep.get(row.stepId) ?? 0) + 1)
  }
  return { lines, errors, perStep }
}

/** A console row's element id: its persisted sequence and its line in that chunk. */
function lineAnchor(id: string) {
  return `build-line-${id.replace(":", "-")}`
}

/**
 * The build transcript: a Pane rather than a Panel, because it is a working
 * region with its own scrolling, not a block of content sitting in the page.
 *
 * It paints its lines the way the dashboard's own restart transcript does
 * (`TranscriptRow`), because a deployment's build is the same three voices:
 * the engine's sentences in the product's face, the commands it ran, and what
 * they printed — BuildKit's numbered steps with the service each builds in
 * its lane's hue, a failure washed where it happened, and plain output in the
 * log console's token colours, so `GET /health 200` reads as it does on the
 * Logs page. Each engine step's lines are a list of their own under a
 * sticky rule naming the step, its mark and how long it took, pushed away by
 * the next step's as the transcript scrolls: the stage picker was the only
 * way to know which step wrote a line.
 *
 * **Live.** While the run is in flight a light runs round the console, new
 * lines are let out a few a frame and rise into place (§11's *arrived*), and
 * following resumes by itself when the reader scrolls back to the end.
 * Scrolled up, searching, filtering or on one stage, lines are simply drawn.
 *
 * The time column is how far into the run a line was written — a build is
 * read in elapsed time, and the wall clock repeated the same second down
 * dozens of lines. The clock is the column's title, and what is copied. A
 * phone has no room for it beside the line, and draws none.
 *
 * From `2xl` the stages are a rail beside the transcript rather than a
 * select above it — chosen once by width (§12), so the picker is in the
 * document once.
 */
export const BuildConsole = memo(function BuildConsole({
  transcript,
  capped,
  steps,
  active,
  outcome,
  operation,
  connected,
  startedAt,
  runNumber,
  stoppedAt,
  selectedStep,
  onSelectStep,
  focusLine,
  flush,
  deployment,
  className,
}: {
  /**
   * The transcript as lines with their counts, kept by the stream as it
   * writes on (`appendTranscript`); the page reads the same copy.
   */
  transcript: Pick<TranscriptBuffer, "rows" | "epoch" | "errors" | "perStep">
  /** The stream kept only its newest events. */
  capped: boolean
  steps: DeploymentStep[]
  active: boolean
  /** How the run ended, for the console's own verdict once it stops. */
  outcome: DeploymentRunState
  /** The run's operation, which names a step a Stop or Restart takes against the live release. */
  operation?: string
  connected: boolean
  /** When the run was claimed: the zero of the time column. */
  startedAt: string
  runNumber: number
  /** When the run ended, past which no step of it is measured. */
  stoppedAt?: string
  selectedStep?: number
  onSelectStep: (id: number | undefined) => void
  /**
   * The persisted line a failure's cause points at. Each new value (the
   * nonce changes on every press) clears the filters that could hide it and
   * scrolls it into the middle of the console.
   */
  focusLine?: { seq: number; nonce: number }
  /** Inside a pane the page draws — the Logs page's Builds view — with no frame of its own. */
  flush?: boolean
  /** The project, for the product a waiting step is drawn as (`StepTile`). */
  deployment?: DeploymentSummary
  /** The console's size where the page decides it; the run page's own height otherwise. */
  className?: string
}) {
  const [query, setQuery] = useState("")
  const [errorsOnly, setErrorsOnly] = useState(false)
  const [follow, setFollow] = useState(true)
  const [wrap, setWrap] = useState(true)
  const [time, setTime] = useState(true)
  const body = useRef<HTMLDivElement>(null)
  // Where following last put the scroll, so a scroll event is read as the
  // reader's only when it went up from there — lines landing between the
  // scroll and its event must not read as the reader leaving the end.
  const followedTo = useRef(0)
  const rail = useBreakpoint("2xl")
  // On a phone the elapsed column is a sixth of a 326px line, and its chip the
  // one the toolbar's second row has no room for.
  const wide = useBreakpoint("sm")
  const stamped = time && wide

  const { rows } = transcript
  // The transcript's epoch moves only when its lines were rebuilt rather than
  // appended to, which is what lets the drip tell a new batch from a stream
  // that was replaced without comparing every line's id.
  const signature = `${transcript.epoch}:${rows.length}`
  const needle = query.trim().toLowerCase()
  // Lines drip in and rise only while the reader is watching the whole
  // transcript arrive; searching, filtering or picking a stage draws them.
  const { drawn, generation, arrivedFrom } = useTranscriptDrip(
    signature,
    rows,
    active && follow && !needle && !errorsOnly && !selectedStep,
    sameEpoch,
  )
  const visible = useMemo(
    () =>
      drawn.filter(
        (row) =>
          (!selectedStep || row.stepId === selectedStep) &&
          (!errorsOnly || row.line.kind === "error") &&
          (!needle || row.line.text.toLowerCase().includes(needle)),
      ),
    [drawn, selectedStep, errorsOnly, needle],
  )
  const { errors, perStep } = transcript
  const stepById = useMemo(() => new Map(steps.map((step) => [step.id, step])), [steps])
  // The visible lines in runs by the step that wrote them, so each run can
  // carry its step's rule — counted over what is shown, so a filtered view
  // still says which step each surviving line came from.
  const groups = useMemo(() => {
    const out: { key: string; step?: DeploymentStep; rows: ConsoleRow[] }[] = []
    for (const row of visible) {
      const last = out.at(-1)
      if (last && last.rows[0].stepId === row.stepId) last.rows.push(row)
      else out.push({ key: row.id, step: stepById.get(row.stepId), rows: [row] })
    }
    return out
  }, [visible, stepById])
  const zero = new Date(startedAt).getTime()
  // Sized from the longest stamp, so a line an hour in does not print over
  // its text. Lines are written in order, so the last is the furthest in.
  const lastTs = rows.at(-1)?.ts
  const stampCh = useMemo(
    () => stampColumnCh(lastTs ? [elapsed(lastTs, zero)] : []),
    [lastTs, zero],
  )
  // A line's number past which it rose into place, for the memoised rows.
  const arrivedAfter = arrivedFrom > 0 ? (drawn[arrivedFrom - 1]?.line.number ?? 0) : 0

  useEffect(() => {
    if (!follow || !body.current) return
    body.current.scrollTop = body.current.scrollHeight
    followedTo.current = body.current.scrollTop
  }, [visible, follow])

  // A hidden console (a phone's pane while another stop is read) has no box,
  // so the lines that land meanwhile cannot be followed. When it is shown
  // again, or resized, its box changes size, and a console that was
  // following goes back to the last line.
  const following = useRef(follow)
  useEffect(() => {
    following.current = follow
  }, [follow])
  useEffect(() => {
    const el = body.current
    if (!el) return
    const observer = new ResizeObserver(() => {
      if (!following.current || el.clientHeight === 0) return
      el.scrollTop = el.scrollHeight
      followedTo.current = el.scrollTop
    })
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  // A search or the errors filter could hide the line; following would pull
  // the console away from it again.
  const [focused, setFocused] = useState<number>()
  if (focusLine && focused !== focusLine.nonce) {
    setFocused(focusLine.nonce)
    setQuery("")
    setErrorsOnly(false)
    setFollow(false)
  }
  const focusSeq = focusLine ? `${focusLine.seq}:` : undefined
  useEffect(() => {
    if (!focusLine) return
    const row = body.current?.querySelector<HTMLElement>(`#${lineAnchor(`${focusLine.seq}:0`)}`)
    row?.scrollIntoView({ block: "center" })
  }, [focusLine, visible])

  const plain = (list: ConsoleRow[]) =>
    list.map((row) => `[${clock(row.ts)}] ${row.line.text}`).join("\n")
  const failed = runFailed(outcome)
  const waiting = active && rows.length === 0

  const stagePicker = rail ? undefined : (
    <Select
      value={selectedStep ? String(selectedStep) : "all"}
      onValueChange={(value) => onSelectStep(value === "all" ? undefined : Number(value))}
    >
      <SelectTrigger
        // Forces a fresh element whenever the selected stage changes, so
        // `autoFocus` fires again: the console is mounted beside Details, so
        // picking a stage there never mounts this trigger for the first
        // time, which is the only moment `autoFocus` normally acts.
        key={selectedStep ?? "all"}
        size="sm"
        aria-label="Build log stage"
        className="order-3 w-32 data-[size=sm]:h-8 sm:order-2 sm:w-44 sm:text-xs sm:data-[size=sm]:h-7"
        autoFocus={selectedStep !== undefined}
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="all">All stages</SelectItem>
        {steps.map((step) => (
          <SelectItem key={step.id} value={String(step.id)}>
            <StepMark state={step.state} />
            {stepName(step.key, operation)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )

  return (
    <Pane flush={flush} className={cn("relative", className ?? "h-[min(70svh,44rem)] min-h-72")}>
      {/* A flush pane's edges are the screen's (a phone's pane), so a beam
          there would run round the window rather than the console. */}
      {active && !flush && <BorderBeam size={96} duration={7} />}
      <PaneHeader className="flex-wrap gap-2 py-2">
        <SearchInput
          dense
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search build logs…"
          aria-label="Search build logs"
          className="max-sm:h-8 max-sm:text-base"
          containerClassName="order-1 min-w-0 flex-1 max-sm:min-w-32 sm:max-w-64"
        />
        {stagePicker}
        <ChipStrip bleed={false} className="order-4 flex-1 sm:order-3 sm:flex-none">
          <FilterChip
            selected={errorsOnly}
            disabled={errors === 0 && !errorsOnly}
            className="disabled:opacity-50 max-sm:h-8"
            onClick={() => setErrorsOnly(!errorsOnly)}
          >
            Errors <ChipCount>{errors}</ChipCount>
          </FilterChip>
          <FilterChip selected={wrap} className="max-sm:h-8" onClick={() => setWrap(!wrap)}>
            Wrap
          </FilterChip>
          <FilterChip selected={time} className="max-sm:hidden" onClick={() => setTime(!time)}>
            Time
          </FilterChip>
          <FilterChip
            selected={follow}
            className="max-sm:h-8"
            onClick={() => {
              setFollow(!follow)
              if (!follow && body.current) body.current.scrollTop = body.current.scrollHeight
            }}
          >
            Follow
          </FilterChip>
        </ChipStrip>
        <span className="order-2 ml-auto flex shrink-0 items-center gap-1.5 sm:order-4">
          <Status
            // How it ended, in the header's own word for it: a rolled-back
            // run is amber "Rolled back" there, not red "Failed".
            tone={
              active ? (connected ? "running" : "warning") : failed ? runTone(outcome) : "stopped"
            }
            label={
              active
                ? connected
                  ? "Live"
                  : "Reconnecting…"
                : failed
                  ? RUN_LABELS[outcome]
                  : "Completed"
            }
            live={active && connected}
            className="mr-1"
          />
          <IconAction
            label="Copy build logs"
            disabled={!visible.length}
            onClick={() => copyText(plain(visible), "Build logs copied")}
          >
            <Copy />
          </IconAction>
          <IconAction
            label="Download build logs"
            disabled={!rows.length}
            onClick={() =>
              saveText(`deployment-${runNumber}.log`, `${plain(rows)}\n`, "text/plain")
            }
          >
            <Download />
          </IconAction>
        </span>
      </PaneHeader>
      {waiting && (
        // Working, and it cannot say how far along (§11 *sweep*): the build
        // has a slot and has not written its first line.
        <div aria-hidden className="relative h-0.5 shrink-0 overflow-hidden bg-plot-brand">
          <span className="absolute inset-y-0 left-0 w-1/3 animate-sweep bg-brand" />
        </div>
      )}

      <div className="flex min-h-0 flex-1">
        {rail && (
          <StageRail
            steps={steps}
            perStep={perStep}
            total={rows.length}
            operation={operation}
            stoppedAt={stoppedAt}
            selected={selectedStep}
            onSelect={onSelectStep}
          />
        )}
        <div
          ref={body}
          onScroll={(event) => {
            const el = event.currentTarget
            // Hidden, it has no box: what the browser reports then is not
            // the reader leaving the end.
            if (el.clientHeight === 0) return
            const atEnd = el.scrollHeight - el.scrollTop - el.clientHeight < 32
            // Following stops the moment the reader scrolls up, or a build
            // still writing yanks them back down with every line; it resumes
            // when they come back to the end.
            if (atEnd) {
              if (!follow) setFollow(true)
            } else if (follow && el.scrollTop < followedTo.current) {
              setFollow(false)
            }
          }}
          className="min-h-0 min-w-0 flex-1 overflow-auto bg-surface-sunken"
        >
          {visible.length ? (
            // A group of lists, one per step: the lines are the only list
            // items, and the step rules between them are for the eye.
            <div
              role="group"
              aria-label="Deployment transcript"
              className={cn("pb-3 font-mono text-xs leading-6", !wrap && "min-w-max")}
            >
              {groups.map((group) => (
                // One step's lines, in a box of their own so its rule stays
                // at the top only while they are on screen and the next
                // step's rule pushes it away.
                <div key={`${generation}:${group.key}`} className="relative">
                  {group.step && (
                    <StepRule
                      step={group.step}
                      operation={operation}
                      lines={perStep.get(group.step.id) ?? 0}
                      stoppedAt={stoppedAt}
                    />
                  )}
                  <ol
                    aria-label={group.step ? stepName(group.step.key, operation) : "Engine"}
                    // A command opens with a rule of its own; directly under
                    // the step's rule that is two lines for one edge.
                    className="[&>li:first-child>ol>li:first-child]:mt-0 [&>li:first-child>ol>li:first-child]:border-t-0"
                  >
                    {blocks(group.rows).map((block) => (
                      <ConsoleBlock
                        key={block[0].id}
                        rows={block}
                        focusSeq={focusSeq}
                        wrap={wrap}
                        needle={needle}
                        arrivedAfter={arrivedAfter}
                        stampCh={stamped ? stampCh : 0}
                        zero={zero}
                      />
                    ))}
                  </ol>
                </div>
              ))}
            </div>
          ) : rows.length === 0 && active ? (
            <Waiting steps={steps} operation={operation} deployment={deployment} />
          ) : (
            <div className="flex h-full min-h-48 items-center justify-center px-6 text-center text-body text-muted-foreground">
              {rows.length
                ? selectedStep && !needle && !errorsOnly
                  ? `${stepName(stepById.get(selectedStep)?.key ?? "this_stage", operation)} wrote nothing to the build log.`
                  : "No lines match. Try another search or stage."
                : "No build output was retained for this run."}
            </div>
          )}
        </div>
      </div>

      <PaneFooter className="justify-between text-hint text-muted-foreground">
        <span className="numeric">
          {visible.length === rows.length
            ? plural(rows.length, "line")
            : `${visible.length.toLocaleString()} of ${plural(rows.length, "line")}`}
          {perStep.size > 0 && ` · ${plural(perStep.size, "step")}`}
          {errors > 0 && <span className="text-destructive"> · {plural(errors, "error")}</span>}
        </span>
        <span>
          {capped
            ? "Showing the latest 5,000 events"
            : active
              ? follow
                ? "Following the newest line"
                : "Paused — scroll to the end to follow"
              : "The whole transcript"}
        </span>
      </PaneFooter>
    </Pane>
  )
})

/** Lines are appended by the transcript and never rebuilt, so the drip compares epochs. */
function sameEpoch(previous: string, next: string) {
  return previous.split(":")[0] === next.split(":")[0]
}

/** How many lines a block of the console holds. */
const BLOCK = 100

const BLOCK_STYLE = {
  contentVisibility: "auto",
  containIntrinsicSize: `auto ${BLOCK * 24}px`,
} as const

/** A step's lines in blocks of `BLOCK`, the last one growing as lines land. */
function blocks(rows: ConsoleRow[]) {
  const out: ConsoleRow[][] = []
  for (let at = 0; at < rows.length; at += BLOCK) out.push(rows.slice(at, at + BLOCK))
  return out
}

type BlockProps = {
  rows: ConsoleRow[]
  focusSeq?: string
  wrap: boolean
  needle: string
  arrivedAfter: number
  stampCh: number
  zero: number
}

/**
 * A hundred lines of a step's list, laid out and painted only while on screen
 * (`content-visibility`), and drawn again only when a line of it changed.
 *
 * A browser lays a list out by walking every child, and three thousand lines
 * in one list cost tens of milliseconds a frame even with each line skipped
 * on its own. In blocks it walks thirty, and the lines of the one block that
 * is growing; a block off screen is not styled or laid out at all. The lines
 * stay in the page — the browser's find, a selection across blocks, Copy and
 * the scroll position all see one transcript — and stay the list's only
 * items: the block and its list are presentation, each line says it is one.
 */
const ConsoleBlock = memo(
  function ConsoleBlock({ rows, focusSeq, wrap, needle, arrivedAfter, stampCh, zero }: BlockProps) {
    return (
      <li
        role="none"
        // One size for every block, guessed from a full one at a line's
        // height until it has been drawn once: a size that followed the
        // growing block's line count restyled the block on every line.
        style={BLOCK_STYLE}
      >
        <ol role="none" className="[&>li:first-child]:mt-0 [&>li:first-child]:border-t-0">
          {rows.map((row) => (
            <ConsoleLine
              key={row.id}
              row={row}
              marked={focusSeq !== undefined && row.id.startsWith(focusSeq)}
              wrap={wrap}
              needle={needle}
              arrived={row.line.number > arrivedAfter}
              stampCh={stampCh}
              zero={zero}
            />
          ))}
        </ol>
      </li>
    )
  },
  // A block's lines are a slice of the transcript, new every render; it is
  // the same block while it starts and ends on the same lines.
  (before, after) =>
    before.rows.length === after.rows.length &&
    before.rows[0] === after.rows[0] &&
    before.rows.at(-1) === after.rows.at(-1) &&
    before.focusSeq === after.focusSeq &&
    before.wrap === after.wrap &&
    before.needle === after.needle &&
    before.arrivedAfter === after.arrivedAfter &&
    before.stampCh === after.stampCh &&
    before.zero === after.zero,
)

/**
 * One line of the console, drawn again only when something about it changed:
 * three thousand lines re-rendered on every frame the drip let one out, and
 * on every second the run's clock ticked, was the console's own lag.
 */
const ConsoleLine = memo(function ConsoleLine({
  row,
  marked,
  wrap,
  needle,
  arrived,
  stampCh,
  zero,
}: {
  row: ConsoleRow
  marked: boolean
  wrap: boolean
  needle: string
  arrived: boolean
  /** The time column's width; 0 draws none. */
  stampCh: number
  /** When the run was claimed, the time column's zero. */
  zero: number
}) {
  return (
    <TranscriptRow
      anchor={lineAnchor(row.id)}
      // Its block is what the browser skips; inside the list's presentation
      // the line says it is one of the step's items.
      inBlock
      marked={marked}
      line={row.line}
      wrap={wrap}
      needle={needle}
      tokens
      arrived={arrived}
      stamp={
        stampCh > 0 ? (
          <time
            dateTime={row.ts}
            title={clock(row.ts)}
            style={{ width: `${stampCh}ch` }}
            className="numeric shrink-0 text-right text-faint select-none"
          >
            {elapsed(row.ts, zero)}
          </time>
        ) : undefined
      }
      trailing={
        row.truncated && (
          <span className="font-sans text-hint text-muted-foreground italic">
            {" "}
            · output truncated
          </span>
        )
      }
    />
  )
})

/**
 * A step's running time on its own clock, so the second that ticks is drawn
 * by the rule and the rail rather than by the console around the lines.
 */
function useStepSeconds(step: DeploymentStep, stoppedAt?: string) {
  const stopped = stoppedAt ? Date.parse(stoppedAt) : NaN
  const now = useNow(1000, step.state === "running" && Number.isNaN(stopped))
  // A finished run's clock stops where the run did, so no step of it reads
  // as still going.
  return stepSeconds(step, Number.isNaN(stopped) ? now : Math.min(now, stopped))
}

/**
 * A run at work that has written nothing yet: the step it is at, drawn as the
 * steps rail draws it, with its name lit the way the fleet's card lights the
 * stage of a release in flight. It was a grey sentence in the middle of a
 * tall empty pane, which read as a console with nothing behind it. It is not a
 * live region: the run's header already announces the stage, and before a step
 * has begun the page says it is waiting for a slot.
 */
function Waiting({
  steps,
  operation,
  deployment,
}: {
  steps: DeploymentStep[]
  operation?: string
  deployment?: DeploymentSummary
}) {
  const working = steps.find((step) => step.state === "running")
  return (
    <div
      data-slot="console-waiting"
      className="flex h-full min-h-48 flex-col items-center justify-center gap-3 px-6 text-center"
    >
      <span data-slot="console-waiting-mark" className="flex">
        {working ? (
          <StepTile step={working} state="running" deployment={deployment} size="md" />
        ) : (
          <WireMark size="md">
            <Logs aria-hidden className="text-muted-foreground" />
          </WireMark>
        )}
      </span>
      <div className="space-y-1">
        <TextShimmer className="text-body font-medium">
          {working ? stepName(working.key, operation) : "Waiting for the first line"}
        </TextShimmer>
        <p className="text-hint text-muted-foreground">
          Nothing written yet. Its lines appear here as they are.
        </p>
      </div>
    </div>
  )
}

/**
 * Where one engine step's lines begin: its mark, its name, how long it took
 * and how many lines it wrote, held at the top of the console while its lines
 * scroll under it. The list of those lines is named after the step, so the
 * rule is for the eye; the transcript's lines stay the only items in it, and
 * copying takes only them.
 */
function StepRule({
  step,
  operation,
  lines,
  stoppedAt,
}: {
  step: DeploymentStep
  operation?: string
  lines: number
  stoppedAt?: string
}) {
  const seconds = useStepSeconds(step, stoppedAt)
  return (
    <div
      aria-hidden
      className="sticky top-0 z-raised mb-1 flex min-w-0 items-center gap-2 border-b border-hairline bg-surface-sunken px-3 py-1 font-sans text-hint sm:px-4 sm:text-xs"
    >
      <StepMark state={step.state} />
      <span
        className={cn(
          "truncate font-medium text-foreground",
          (step.state === "failed" || step.state === "blocked") && "text-destructive",
        )}
      >
        {stepName(step.key, operation)}
      </span>
      {seconds !== undefined && (
        <span className="numeric shrink-0 text-hint text-muted-foreground">
          {duration(seconds)}
        </span>
      )}
      <span className="numeric ml-auto shrink-0 text-hint text-muted-foreground">
        {plural(lines, "line")}
      </span>
    </div>
  )
}

/**
 * The stages beside the transcript at widths where both fit: every step the
 * run took, its mark, how long it took and how much it wrote, the picked one
 * filled as every selection is (§3).
 *
 * The rail is mounted beside Details, so a stage picked there is not a mount
 * and `autoFocus` would not fire: the picked stage takes the keyboard whenever
 * the pick changes, as the select it replaces does.
 */
function StageRail({
  steps,
  perStep,
  total,
  operation,
  stoppedAt,
  selected,
  onSelect,
}: {
  steps: DeploymentStep[]
  perStep: Map<number, number>
  total: number
  operation?: string
  stoppedAt?: string
  selected?: number
  onSelect: (id: number | undefined) => void
}) {
  const buttons = useRef(new Map<number, HTMLButtonElement>())
  useEffect(() => {
    if (selected) buttons.current.get(selected)?.focus()
  }, [selected])
  const row = (pressed: boolean) =>
    cn(
      "flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs focus-ring-inset transition-colors",
      pressed ? "bg-accent text-foreground" : "text-muted-foreground hover:bg-row-hover",
    )
  return (
    <div
      role="group"
      aria-label="Build log stage"
      className="w-60 shrink-0 space-y-0.5 overflow-y-auto border-r border-hairline p-2"
    >
      <button
        type="button"
        aria-pressed={!selected}
        onClick={() => onSelect(undefined)}
        className={row(!selected)}
      >
        <span className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1 truncate font-medium">All stages</span>
        <span className="numeric shrink-0 text-hint text-muted-foreground">{total}</span>
      </button>
      {steps.map((step) => (
        <StageButton
          key={step.id}
          step={step}
          lines={perStep.get(step.id) ?? 0}
          operation={operation}
          stoppedAt={stoppedAt}
          pressed={selected === step.id}
          className={row(selected === step.id)}
          onSelect={() => onSelect(step.id)}
          buttonRef={(element) => {
            if (element) buttons.current.set(step.id, element)
            else buttons.current.delete(step.id)
          }}
        />
      ))}
    </div>
  )
}

/** One stage on the rail, with its own clock while it runs. */
function StageButton({
  step,
  lines,
  operation,
  stoppedAt,
  pressed,
  className,
  onSelect,
  buttonRef,
}: {
  step: DeploymentStep
  lines: number
  operation?: string
  stoppedAt?: string
  pressed: boolean
  className: string
  onSelect: () => void
  buttonRef: (element: HTMLButtonElement | null) => void
}) {
  const seconds = useStepSeconds(step, stoppedAt)
  return (
    <button
      ref={buttonRef}
      type="button"
      aria-pressed={pressed}
      onClick={onSelect}
      className={className}
    >
      <StepMark state={step.state} />
      <span
        className={cn(
          "min-w-0 flex-1 truncate",
          pressed || lines > 0 ? "text-foreground" : "",
          (step.state === "failed" || step.state === "blocked") && "text-destructive",
        )}
      >
        {stepName(step.key, operation)}
      </span>
      {seconds !== undefined && (
        <span className="numeric shrink-0 text-hint text-muted-foreground">
          {duration(seconds)}
        </span>
      )}
      <span
        className={cn(
          "numeric w-6 shrink-0 text-right text-hint",
          lines ? "text-muted-foreground" : "text-faint",
        )}
      >
        {lines}
      </span>
    </button>
  )
}
