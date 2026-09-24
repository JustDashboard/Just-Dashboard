"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { Copy, Download } from "@/components/icons"
import { Pane, PaneFooter, PaneHeader } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { IconAction } from "@/components/icon-action"
import { Status } from "@/components/status-dot"
import { TranscriptRow, useTranscriptDrip } from "@/components/transcript-line"
import { BorderBeam } from "@/components/ui/border-beam"
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
  formatDuration,
  runFailed,
  runTone,
  stepName,
  stepSeconds,
} from "@/components/deploy/vocabulary"
import { useMediaQuery } from "@/hooks/use-mobile"
import { clock, plural } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { downloadText } from "@/lib/metrics-export"
import { transcriptLine, type TranscriptLine } from "@/lib/transcript"
import { cn } from "@/lib/utils"
import type { DeploymentRunState, DeploymentStep } from "@/lib/types"

/**
 * One `step.log` event as the stream delivers it: a chunk, not a line, from
 * the step that wrote it, on the stream it was written to — `status` for the
 * engine's own sentences, `stdout`/`stderr` for what the step ran.
 */
export type BuildLogEvent = {
  seq: number
  stepId: number
  ts: string
  stream: string
  text: string
  truncated: boolean
}

/** A line of the console: the transcript's shape, plus where and when it was written. */
export type ConsoleRow = {
  id: string
  stepId: number
  ts: string
  truncated: boolean
  line: TranscriptLine
}

/**
 * Persisted events are chunks, not lines. Each displayed line keeps its
 * event's identity and gets a stable number, including when one chunk holds
 * several lines, and is read into the shapes `lib/transcript.ts` knows —
 * except that a line on the `status` stream is the engine's own voice
 * ("Resolved main to a12bc34") whatever it looks like, which only the stream
 * can say.
 */
export function consoleRows(events: BuildLogEvent[]): ConsoleRow[] {
  let number = 0
  return events.flatMap((event) => {
    // Terminal escape sequences are presentation, never markup from the build.
    const clean = event.text.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "").replace(/\r\n?/g, "\n")
    const parts = clean.replace(/\n$/, "").split("\n")
    return parts.map((text, index) => {
      number += 1
      return {
        id: `${event.seq}:${index}`,
        stepId: event.stepId,
        ts: event.ts,
        // The engine marks the chunk; the note goes after its last line.
        truncated: event.truncated && index === parts.length - 1,
        line: transcriptLine(number, text, event.stream === "status"),
      }
    })
  })
}

/**
 * The transcript in three numbers, for the console's own counts and the
 * readings outside it — the view strip's count and its error mark, and
 * Details' lines per step. Read once from the parsed rows, by the page.
 */
export function transcriptSummary(rows: ConsoleRow[]) {
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

/** "+1:04", "+1:02:09": how far into the run a line was written. */
function elapsed(ts: string, from: number) {
  const seconds = Math.max(0, Math.round((new Date(ts).getTime() - from) / 1000))
  if (Number.isNaN(seconds)) return ""
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = String(seconds % 60).padStart(2, "0")
  return h > 0 ? `+${h}:${String(m).padStart(2, "0")}:${s}` : `+${m}:${s}`
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
export function BuildConsole({
  rows,
  summary,
  capped,
  steps,
  active,
  outcome,
  connected,
  startedAt,
  runNumber,
  now,
  selectedStep,
  onSelectStep,
  hidden,
}: {
  /** The transcript as lines, read once by the page (`consoleRows`). */
  rows: ConsoleRow[]
  /** Those lines' counts (`transcriptSummary`), which the page reads too. */
  summary: ReturnType<typeof transcriptSummary>
  /** The stream kept only its newest events. */
  capped: boolean
  steps: DeploymentStep[]
  active: boolean
  /** How the run ended, for the console's own verdict once it stops. */
  outcome: DeploymentRunState
  connected: boolean
  /** When the run was claimed: the zero of the time column. */
  startedAt: string
  runNumber: number
  now: number
  selectedStep?: number
  onSelectStep: (id: number | undefined) => void
  /** Keeps the console mounted while another run view is active, so its
   * search, wrap, follow and scroll position survive switching back. */
  hidden?: boolean
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
  const rail = useMediaQuery("(min-width: 1536px)")
  // On a phone the elapsed column is 48px of a 326px line, and its chip the
  // one the toolbar's second row has no room for.
  const wide = useMediaQuery("(min-width: 640px)")
  const stamped = time && wide

  // Grows by appending while events arrive in order, which is what lets the
  // drip tell a new batch from a stream that was replaced.
  const signature = useMemo(() => rows.map((row) => `${row.id},`).join(""), [rows])
  const needle = query.trim().toLowerCase()
  // Lines drip in and rise only while the reader is watching the whole
  // transcript arrive; searching, filtering or picking a stage draws them.
  const { drawn, generation, arrivedFrom } = useTranscriptDrip(
    signature,
    rows,
    active && follow && !needle && !errorsOnly && !selectedStep,
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
  const { errors, perStep } = summary
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

  useEffect(() => {
    if (!follow || !body.current) return
    body.current.scrollTop = body.current.scrollHeight
    followedTo.current = body.current.scrollTop
  }, [visible, follow])

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
        // `autoFocus` fires again: with the console kept permanently mounted,
        // picking a stage from Details no longer mounts this trigger for the
        // first time, which is the only moment `autoFocus` normally acts.
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
            {stepName(step.key)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )

  return (
    <Pane className="relative h-[min(70vh,44rem)] min-h-72" hidden={hidden}>
      {active && <BorderBeam size={96} duration={7} />}
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
        <ChipStrip className="order-4 flex-1 max-sm:mx-0 max-sm:px-0 sm:order-3 sm:flex-none">
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
              downloadText(`deployment-${runNumber}.log`, `${plain(rows)}\n`, "text/plain")
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
            now={now}
            selected={selectedStep}
            onSelect={onSelectStep}
          />
        )}
        <div
          ref={body}
          onScroll={(event) => {
            const el = event.currentTarget
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
                    <StepRule step={group.step} lines={perStep.get(group.step.id) ?? 0} now={now} />
                  )}
                  <ol
                    aria-label={group.step ? stepName(group.step.key) : "Engine"}
                    // A command opens with a rule of its own; directly under
                    // the step's rule that is two lines for one edge.
                    className="[&>li:first-child]:mt-0 [&>li:first-child]:border-t-0"
                  >
                    {group.rows.map((row) => (
                      <TranscriptRow
                        key={row.id}
                        line={row.line}
                        wrap={wrap}
                        needle={needle}
                        tokens
                        arrived={row.line.number > arrivedFrom}
                        stamp={
                          stamped ? (
                            <time
                              dateTime={row.ts}
                              title={clock(row.ts)}
                              className="numeric w-12 shrink-0 text-right text-muted-foreground/60 select-none"
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
                    ))}
                  </ol>
                </div>
              ))}
            </div>
          ) : (
            <div className="flex h-full min-h-48 items-center justify-center px-6 text-center text-body text-muted-foreground">
              {rows.length
                ? selectedStep && !needle && !errorsOnly
                  ? `${stepName(stepById.get(selectedStep)?.key ?? "this_stage")} wrote nothing to the build log.`
                  : "No lines match. Try another search or stage."
                : active
                  ? "Waiting for build output. New lines appear here automatically."
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
}

/**
 * Where one engine step's lines begin: its mark, its name, how long it took
 * and how many lines it wrote, held at the top of the console while its lines
 * scroll under it. The list of those lines is named after the step, so the
 * rule is for the eye; the transcript's lines stay the only items in it, and
 * copying takes only them.
 */
function StepRule({ step, lines, now }: { step: DeploymentStep; lines: number; now: number }) {
  const seconds = stepSeconds(step, now)
  return (
    <div
      aria-hidden
      className="sticky top-0 z-10 mb-1 flex min-w-0 items-center gap-2 border-b border-hairline bg-surface-sunken px-3 py-1 font-sans text-hint sm:px-4 sm:text-xs"
    >
      <StepMark state={step.state} />
      <span
        className={cn(
          "truncate font-medium text-foreground",
          (step.state === "failed" || step.state === "blocked") && "text-destructive",
        )}
      >
        {stepName(step.key)}
      </span>
      {seconds !== undefined && (
        <span className="numeric shrink-0 text-hint text-muted-foreground">
          {formatDuration(seconds)}
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
 * The rail stays mounted while Details is open, so a stage picked there is not
 * a mount and `autoFocus` would not fire: the picked stage takes the keyboard
 * whenever the pick changes, as the select it replaces does.
 */
function StageRail({
  steps,
  perStep,
  total,
  now,
  selected,
  onSelect,
}: {
  steps: DeploymentStep[]
  perStep: Map<number, number>
  total: number
  now: number
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
      {steps.map((step) => {
        const seconds = stepSeconds(step, now)
        const lines = perStep.get(step.id) ?? 0
        const pressed = selected === step.id
        return (
          <button
            key={step.id}
            ref={(element) => {
              if (element) buttons.current.set(step.id, element)
              else buttons.current.delete(step.id)
            }}
            type="button"
            aria-pressed={pressed}
            onClick={() => onSelect(step.id)}
            className={row(pressed)}
          >
            <StepMark state={step.state} />
            <span
              className={cn(
                "min-w-0 flex-1 truncate",
                pressed || lines > 0 ? "text-foreground" : "",
                (step.state === "failed" || step.state === "blocked") && "text-destructive",
              )}
            >
              {stepName(step.key)}
            </span>
            {seconds !== undefined && (
              <span className="numeric shrink-0 text-hint text-muted-foreground">
                {formatDuration(seconds)}
              </span>
            )}
            <span
              className={cn(
                "numeric w-6 shrink-0 text-right text-hint",
                lines ? "text-muted-foreground" : "text-muted-foreground/40",
              )}
            >
              {lines}
            </span>
          </button>
        )
      })}
    </div>
  )
}
