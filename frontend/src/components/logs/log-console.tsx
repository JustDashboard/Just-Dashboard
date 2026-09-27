"use client"

import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import {
  Backspace,
  BlendMode,
  Check,
  ChevronDoubleDown,
  ChevronRight,
  Clock,
  CodeWrap,
  Copy,
  Layers,
  Pause,
  Play,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { plural, timestamp } from "@/lib/format"
import type { LogLine } from "@/lib/types"
import {
  LEVEL_EDGE,
  LEVEL_MARK,
  LEVEL_TEXT,
  LEVEL_TONE,
  highlightRanges,
  lineValue,
  segmentLine,
  type LogLevel,
} from "@/lib/log-filter"
import { eventMeta } from "@/lib/log-lenses"
import { fieldOf, type LogFieldKind } from "@/lib/log-fields"
import {
  EVENT_WORD,
  LEVEL_WORD,
  LogText,
  laneStyle,
  structuredText,
} from "@/components/logs/log-text"
import { FieldValue } from "@/components/logs/field-value"
import { eventColumnFor } from "@/components/logs/logs-model"
import { LOG_TIMES, TIME_WIDTH, formatLogTime, setLogView, useLogView } from "@/lib/log-view"
import type { LogTime } from "@/lib/log-view"
import { useMetrics } from "@/hooks/use-metrics"
import type { LogFilterState } from "@/components/logs/types"
import { PaneFooter } from "@/components/panel"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { copyText } from "@/lib/clipboard"
import { logLineKey } from "@/lib/log-line-key"

function lineToText(line: LogLine, withTime: boolean) {
  const stamp = withTime && line.timestamp ? `${timestamp(line.timestamp)} ` : ""
  return `${stamp}${line.text}`
}

/**
 * The run of a stack trace shown before the rest folds away. A run one line
 * longer is shown whole: a fold that hides one line costs a line to say so.
 */
const FOLD_AFTER = 3

type Row =
  | {
      kind: "line"
      key: number
      line: LogLine
      /** The stamp of the row drawn above, for the time column's `delta`. */
      prev?: string
      /** The record this line continues, when it is part of one. */
      head?: LogLine
      /** How many identical lines this row stands for, and when the first was. */
      repeat?: number
      since?: string
    }
  | { kind: "fold"; key: string; head: number; hidden: number; expanded: boolean }
  | { kind: "divider"; key: string; line: LogLine; label: string }

/**
 * What a line is compared on to call it a repeat: who said it, how loudly,
 * what it records, and the words — without the time the line itself leads
 * with, which is the one part a repeat never repeats.
 */
const LEADING_TIME =
  /^\[?(?:\d{4}[-/.]\d{2}[-/.]\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?|[A-Z][a-z]{2} +\d{1,2} \d{2}:\d{2}:\d{2}(?:\.\d+)?)\]?\s*/

function sameAs(a: LogLine, b: LogLine) {
  return (
    a.source === b.source &&
    a.level === b.level &&
    a.event === b.event &&
    (a.message ?? a.text.replace(LEADING_TIME, "")) ===
      (b.message ?? b.text.replace(LEADING_TIME, ""))
  )
}

function hasHit(line: LogLine, filter: LogFilterState | undefined) {
  if (line.match?.length) return true
  return filter ? highlightRanges(line.text, filter).length > 0 : false
}

/**
 * The rows the lines are drawn as. A record's continuation lines stay under
 * their head — the first few inline, the rest behind a fold unless one of
 * them is what the search found — a lens's lifecycle events get a rule across
 * the pane, and in the live tail a run of identical lines is one row with its
 * count. An orphan continuation at the top of the window (its head scrolled
 * out of the buffer) is drawn as the line it is.
 */
function buildRows(
  lines: LogLine[],
  opts: {
    dedupe: boolean
    lens?: string
    folds: ReadonlySet<number>
    filter?: LogFilterState
  },
): Row[] {
  const rows: Row[] = []
  let prev: string | undefined
  let head: { line: LogLine; key: number } | undefined
  let last: Extract<Row, { kind: "line" }> | undefined
  let lastHasRun = false

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    if (line.cont && head) {
      let end = i
      while (end < lines.length && lines[end].cont) end++
      const run = lines.slice(i, end)
      const long = run.length > FOLD_AFTER + 1
      const expanded = long && opts.folds.has(head.key)
      // A fold that hides what the search found is a result nobody sees.
      const open = !long || expanded || run.slice(FOLD_AFTER).some((l) => hasHit(l, opts.filter))
      const shown = open ? run : run.slice(0, FOLD_AFTER)
      for (const cont of shown) {
        rows.push({ kind: "line", key: logLineKey(cont), line: cont, prev, head: head.line })
        prev = cont.timestamp ?? prev
      }
      if (long && (expanded || !open)) {
        rows.push({
          kind: "fold",
          key: `fold:${head.key}`,
          head: head.key,
          hidden: run.length - FOLD_AFTER,
          expanded,
        })
      }
      lastHasRun = true
      last = undefined
      i = end - 1
      continue
    }

    const key = logLineKey(line)
    const startsRun = Boolean(lines[i + 1]?.cont)
    if (opts.dedupe && last && !lastHasRun && !startsRun && sameAs(last.line, line)) {
      last.repeat = (last.repeat ?? 1) + 1
      last.since ??= last.line.timestamp
      last.line = line
      last.key = key
      head = { line, key }
      continue
    }

    const meta = line.event ? eventMeta(opts.lens, line.event, line.lens) : undefined
    if (meta?.divider) {
      rows.push({ kind: "divider", key: `divider:${key}`, line, label: meta.label })
    }
    last = { kind: "line", key, line, prev }
    rows.push(last)
    prev = line.timestamp ?? prev
    head = { line, key }
    lastHasRun = false
  }
  return rows
}

/** The event word a line draws in the level column, if its lens names one worth drawing. */
function markable(line: LogLine, lens: string | undefined) {
  if (!line.event || line.cont) return undefined
  const meta = eventMeta(lens, line.event, line.lens)
  return meta && meta.mark !== false ? meta : undefined
}

// The level column widens for event words only while a line on screen has
// one, as the file column appears only for a rotated set: a column of blanks
// is width taken from the text.
export { eventColumnFor }

const COLUMN_WIDTH: Partial<Record<LogFieldKind, string>> = {
  address: "w-28",
  duration: "w-14",
  status: "w-10",
  method: "w-20",
  code: "w-16",
}

/**
 * The lines, and the chrome that belongs to them.
 *
 * Not a surface of its own any more: it is the body of the workspace pane,
 * between the filter rows above and the footer below, so the lines sit on the
 * same ground as the controls that narrow them and nothing draws a second
 * frame inside the first. `leading` is what the workspace puts at the left of
 * the strip above the lines — the level chips — and the view toggles take the
 * right of the same strip.
 *
 * Two things are load-bearing and easy to undo.
 *
 * **Following stops the moment the reader scrolls up**, and resumes when they
 * return to the bottom. Nothing is more frustrating than losing the line you
 * were reading to an autoscroll, and a viewer that needs a button pressed
 * before it will hold still is one people stop trusting.
 *
 * **Pausing holds the incoming lines rather than dropping them.** The old pane
 * called autoscroll "Paused", which meant that on a chatty log the only way to
 * read anything was to lose everything written while you read. Held lines are
 * counted and appended on resume, so pausing costs nothing.
 *
 * A line opens in place, under itself, the way a request row does: a press is
 * a question about that line, asked while scanning the ones around it, and a
 * drawer over the page would make the reader close it to ask about the next.
 * The press only counts when nothing was selected — dragging across a line
 * to copy part of it is the other thing a reader does with a log. `j` and `k`
 * walk the lines and Enter opens one, while the lines have focus.
 *
 * Rendering leans on `content-visibility` rather than a virtualiser. The
 * browser skips layout and paint for the rows outside the viewport, which is
 * what a virtualiser is for, while the scrollbar stays honest, wrapped rows
 * keep their real heights and the browser's own find still works — three
 * things a windowed list gives up.
 */
export function LogConsole({
  lines,
  filter,
  className,
  leading,
  status,
  empty,
  footer,
  showLineNumbers,
  showSource,
  showFile,
  paused,
  onPausedChange,
  held = 0,
  onClear,
  lens,
  columns,
  collapseRepeats,
  renderDetail,
}: {
  lines: LogLine[]
  filter: LogFilterState
  className?: string
  /** The left of the strip above the lines: the level chips. */
  leading?: React.ReactNode
  /** The footer's first words: the stream's state, or the search's summary. */
  status?: React.ReactNode
  empty: React.ReactNode
  /** Anything else the footer carries after the status — search notes, retention. */
  footer?: React.ReactNode
  showLineNumbers?: boolean
  /**
   * Whether a line's own source is worth a column. For a file it is the file
   * you already picked in the rail, repeated on every line; for the journal it
   * is which unit spoke, which is the whole reason to read the journal.
   */
  showSource?: boolean
  /** Which file of a rotated set a search result came from. */
  showFile?: boolean
  paused?: boolean
  onPausedChange?: (paused: boolean) => void
  held?: number
  onClear?: () => void
  /** The lens the lines were read through, which names their events. */
  lens?: string
  /**
   * Values the lens reads out of the text that the text buries — a Postgres
   * line's duration, user and database. A stable array: it is a row's prop.
   */
  columns?: string[]
  /** Whether a run of identical lines may collapse — the live tail, never History. */
  collapseRepeats?: boolean
  /** What opens under a line; `head` is the record a continuation line belongs to. */
  renderDetail?: (line: LogLine, head: LogLine | undefined) => React.ReactNode
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [following, setFollowing] = useState(true)
  const [open, setOpen] = useState<number | null>(null)
  const [active, setActive] = useState<number | null>(null)
  const [folds, setFolds] = useState<ReadonlySet<number>>(() => new Set())
  const { wrap, time, highlight, dedupe } = useLogView()
  const hostname = useMetrics().host?.hostname
  const showTime = time !== "off"

  const toBottom = useCallback(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [])

  // With no lines the pane holds a sentence, read from its start.
  useLayoutEffect(() => {
    if (following && lines.length > 0) toBottom()
  }, [lines, following, toBottom])

  // The opened line's detail is as wide as the pane, not as the longest line:
  // unwrapped, the lines block is as wide as its widest line and a detail
  // inside it would be too.
  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const observer = new ResizeObserver(() => {
      el.style.setProperty("--console-w", `${el.clientWidth}px`)
    })
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight < 40)
  }

  const copyAll = () =>
    copyText(
      lines.map((l) => lineToText(l, showTime)).join("\n"),
      `Copied ${plural(lines.length, "line")}`,
    )

  // Ranges come from the server for a search — it can re-run its own regular
  // expression and the browser cannot — and are worked out here for a live
  // tail, where computing them per line on the streaming path would be work
  // for lines nobody scrolls back to.
  const needsClientHighlight = useMemo(
    () => filter.q !== "" && !lines.some((l) => l.match?.length),
    [filter.q, lines],
  )
  const clientFilter = needsClientHighlight ? filter : undefined

  const rows = useMemo(
    () =>
      buildRows(lines, {
        dedupe: Boolean(collapseRepeats && dedupe),
        lens,
        folds,
        filter: clientFilter,
      }),
    [lines, collapseRepeats, dedupe, lens, folds, clientFilter],
  )
  const eventColumn = useMemo(() => eventColumnFor(lines, lens), [lines, lens])
  // A lens column no line on screen has a value for is not drawn either. Keyed
  // on the names, so the array a row receives changes only when the set does.
  const shownKey = useMemo(
    () => (columns ?? []).filter((key) => lines.some((l) => lineValue(l, key))).join(","),
    [columns, lines],
  )
  const shownColumns = useMemo(() => (shownKey ? shownKey.split(",") : undefined), [shownKey])
  const lineRows = useMemo(
    () => rows.filter((r): r is Extract<Row, { kind: "line" }> => r.kind === "line"),
    [rows],
  )

  const press = useCallback((key: number) => {
    // A drag across the text is a copy, not a press.
    if (window.getSelection()?.isCollapsed === false) return
    setActive(key)
    setOpen((current) => (current === key ? null : key))
  }, [])

  const toggleFold = useCallback((head: number) => {
    setFolds((current) => {
      const next = new Set(current)
      if (next.has(head)) next.delete(head)
      else next.add(head)
      return next
    })
  }, [])

  const focusRow = (key: number) => {
    const el = scrollRef.current?.querySelector<HTMLElement>(`[data-row-key="${key}"]`)
    el?.focus()
    el?.scrollIntoView({ block: "nearest" })
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const target = e.target as HTMLElement
    // Keys typed inside an opened line's detail are that control's.
    if (target.closest("[data-line-detail]") || e.metaKey || e.ctrlKey || e.altKey) return
    if (e.key === "j" || e.key === "k") {
      if (lineRows.length === 0) return
      e.preventDefault()
      const at = lineRows.findIndex((r) => r.key === active)
      const next =
        at < 0
          ? e.key === "j"
            ? 0
            : lineRows.length - 1
          : Math.max(0, Math.min(lineRows.length - 1, at + (e.key === "j" ? 1 : -1)))
      const key = lineRows[next].key
      setActive(key)
      focusRow(key)
    } else if (e.key === "Enter" && active !== null) {
      e.preventDefault()
      setOpen((current) => (current === active ? null : active))
    } else if (e.key === "Escape" && open !== null) {
      e.preventDefault()
      setOpen(null)
    }
  }

  return (
    <div className={cn("@container flex min-h-0 min-w-0 flex-1 flex-col", className)}>
      {/* One row that scrolls sideways rather than wrapping: on a phone the
          chips are the thing to reach, and two more rows of chrome above the
          lines were two more rows of lines lost. The chips scroll and the
          toggles stay: in a sheet's narrow column the chips pushed Wrap off
          the end, and it is the toggle a narrow pane needs most. */}
      <div className="flex min-h-9 shrink-0 items-center gap-1 border-b border-hairline px-2 py-1">
        <div className="scroll-affordance flex min-w-0 flex-1 [scrollbar-width:none] items-center overflow-x-auto [&::-webkit-scrollbar]:hidden">
          {leading}
        </div>
        {onPausedChange && (
          <ToolbarToggle
            active={paused}
            onClick={() => onPausedChange(!paused)}
            icon={paused ? Play : Pause}
            label={paused ? "Resume" : "Pause"}
            hint={paused ? "Append what arrived while paused" : "Hold new lines while you read"}
            // The held count stays beside the glyph at every width: it is the
            // one thing a paused pane has to say.
            extra={
              paused && held > 0 ? (
                <span className="numeric">{held.toLocaleString()} held</span>
              ) : undefined
            }
          />
        )}
        <ToolbarToggle
          active={wrap}
          onClick={() => setLogView({ wrap: !wrap })}
          icon={CodeWrap}
          label="Wrap"
          hint="Wrap long lines instead of scrolling sideways"
        />
        <TimeMenu time={time} />
        <ToolbarToggle
          active={highlight}
          onClick={() => setLogView({ highlight: !highlight })}
          icon={BlendMode}
          label="Colour"
          hint="Colour each line by what is in it, and show structured lines as their message and fields. Off shows every line exactly as written."
        />
        {collapseRepeats && (
          <ToolbarToggle
            active={dedupe}
            onClick={() => setLogView({ dedupe: !dedupe })}
            icon={Layers}
            label="Repeats"
            hint="Collapse a run of identical lines into one with its count"
          />
        )}
        <ToolbarToggle
          onClick={copyAll}
          icon={Copy}
          label="Copy"
          hint="Copy every line in this pane. Open a line to copy just that one."
        />
        {onClear && (
          <ToolbarToggle onClick={onClear} icon={Backspace} label="Clear" hint="Empty the pane" />
        )}
      </div>

      <div
        ref={scrollRef}
        onScroll={onScroll}
        onKeyDown={onKeyDown}
        tabIndex={0}
        aria-label="Log lines"
        className="@container min-h-0 flex-1 overflow-auto bg-surface-sunken font-mono text-xs leading-relaxed focus-ring-inset"
      >
        {lines.length === 0 ? (
          // At least the pane's height, so the sentence sits in the middle,
          // and taller when a phone's chrome leaves the pane less than the
          // sentence needs: it scrolls rather than losing its first lines.
          <div className="flex min-h-full items-center justify-center p-6">{empty}</div>
        ) : (
          // Keyed on arrival so the block rises once when the first lines
          // land and then holds still while the tail appends to it.
          <div key="lines" className={cn("animate-rise py-1.5", !wrap && "w-max min-w-full")}>
            {rows.map((row) => {
              if (row.kind === "divider") {
                return <Divider key={row.key} line={row.line} label={row.label} time={time} />
              }
              if (row.kind === "fold") {
                return (
                  <button
                    key={row.key}
                    type="button"
                    onClick={() => toggleFold(row.head)}
                    className="flex w-full items-center gap-1.5 py-px pr-4 pl-6 text-left text-muted-foreground focus-ring-inset transition-colors hover:bg-row-hover hover:text-foreground"
                  >
                    <ChevronRight
                      aria-hidden
                      className={cn("size-3 transition-transform", row.expanded && "-rotate-90")}
                    />
                    <span className="numeric">
                      {row.expanded
                        ? `Fold the last ${row.hidden.toLocaleString()}`
                        : `${row.hidden.toLocaleString()} more ${row.hidden === 1 ? "line" : "lines"}`}
                    </span>
                  </button>
                )
              }
              return (
                <LineWithDetail
                  key={row.key}
                  row={row}
                  open={open === row.key}
                  onPress={press}
                  time={time}
                  showLineNumbers={showLineNumbers}
                  showFile={showFile}
                  showSource={showSource}
                  wrap={wrap}
                  highlight={highlight}
                  hostname={hostname}
                  filter={clientFilter}
                  lens={lens}
                  eventColumn={eventColumn}
                  columns={shownColumns}
                  renderDetail={renderDetail}
                />
              )
            })}
          </div>
        )}
      </div>

      {!following && lines.length > 0 && (
        <button
          className="flex items-center justify-center gap-1.5 border-t border-hairline py-1.5 text-xs text-muted-foreground focus-ring-inset transition-colors hover:bg-row-hover hover:text-foreground"
          onClick={() => {
            setFollowing(true)
            toBottom()
          }}
        >
          <ChevronDoubleDown className="size-3" />
          Jump to the end
          {held > 0 && <span className="numeric">· {held.toLocaleString()} held</span>}
        </button>
      )}

      <PaneFooter className="gap-x-4 gap-y-1 px-3 text-hint text-muted-foreground">
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          {status}
          <span className="numeric whitespace-nowrap">{plural(lines.length, "line")}</span>
        </span>
        {footer}
      </PaneFooter>
    </div>
  )
}

function LineWithDetail({
  row,
  open,
  renderDetail,
  ...props
}: Omit<LineProps, "line" | "rowKey" | "prev" | "repeat" | "since" | "cont"> & {
  row: Extract<Row, { kind: "line" }>
  renderDetail?: (line: LogLine, head: LogLine | undefined) => React.ReactNode
}) {
  return (
    <>
      <LogRow
        {...props}
        line={row.line}
        rowKey={row.key}
        prev={row.prev}
        repeat={row.repeat}
        since={row.since}
        cont={Boolean(row.head)}
        open={open}
      />
      {open && renderDetail && (
        // Sticky at the pane's left edge and as wide as the pane, so an
        // unwrapped page of long lines does not carry the detail off-screen.
        <div data-line-detail className="sticky left-0 w-(--console-w) font-sans">
          {renderDetail(row.line, row.head)}
        </div>
      )}
    </>
  )
}

/**
 * A lifecycle event as a rule across the pane — a unit started, an app came
 * up — so the runs of a service read as runs rather than as one long log.
 */
function Divider({ line, label, time }: { line: LogLine; label: string; time: LogTime }) {
  const name = line.attrs?.unit ?? line.attrs?.service ?? line.attrs?.program
  return (
    <div
      role="separator"
      aria-label={[label, name].filter(Boolean).join(" ")}
      className="flex items-center gap-2 pt-2 pr-4 pb-0.5 pl-3 font-sans text-hint text-muted-foreground select-none"
    >
      <span aria-hidden className="h-px w-3 shrink-0 bg-hairline" />
      <span className="shrink-0 font-medium">{label}</span>
      {name && (
        <span className="max-w-60 shrink-0 truncate" style={laneStyle(name)}>
          {name}
        </span>
      )}
      {line.timestamp && time !== "off" && (
        <span className="numeric shrink-0">
          {formatLogTime(line.timestamp, time === "delta" ? "clock" : time)}
        </span>
      )}
      <span aria-hidden className="h-px min-w-6 flex-1 bg-hairline" />
    </div>
  )
}

type LineProps = {
  line: LogLine
  rowKey?: number
  open?: boolean
  onPress?: (key: number) => void
  time: LogTime
  prev?: string
  showLineNumbers?: boolean
  showFile?: boolean
  showSource?: boolean
  wrap: boolean
  highlight: boolean
  hostname?: string
  /** Set when the hits have to be found here rather than sent by the server. */
  filter?: LogFilterState
  lens?: string
  /** The event column's width in characters (`eventColumnFor`); none, the level's own. */
  eventColumn?: number | false
  columns?: string[]
  repeat?: number
  since?: string
  /** A continuation line: its head already carries the level and the event. */
  cont?: boolean
}

/**
 * One line of the pane.
 *
 * Memoised on its own props because the live tail appends: without it every
 * arriving line redrew the four thousand above it, and a coloured line is a
 * dozen spans rather than one text node. Every prop is a primitive, a line
 * object the buffer keeps, or something drawn from the lens registry, so an
 * append redraws only the rows it adds.
 *
 * Coloured (`highlight`), a line is its shapes: the event the lens named — or
 * the level, where it named none — in its own colour in a column of its own,
 * an error or a warning washed across the row the way the build console
 * washes a failing step — found by scrolling, not by reading — and a
 * structured line drawn as its message and fields rather than as JSON.
 * Uncoloured it is exactly the text that was written, with the level's tint on
 * it as before.
 */
export const LogRow = memo(function LogRow({
  line,
  rowKey,
  open,
  onPress,
  time,
  prev,
  showLineNumbers,
  showFile,
  showSource,
  wrap,
  highlight,
  hostname,
  filter,
  lens,
  eventColumn,
  columns,
  repeat,
  since,
  cont,
}: LineProps) {
  const level = (line.level ?? "") as LogLevel
  const structured = highlight ? structuredText(line) : null
  const shown = structured ?? line.text
  const ranges =
    line.match?.length && !structured
      ? line.match
      : filter
        ? highlightRanges(shown, filter)
        : undefined
  const loud = highlight && (level === "critical" || level === "error")
  const warned = highlight && level === "warn"
  const event = markable(line, lens)
  const showTime = time !== "off"
  const flow = wrap ? "wrap-anywhere whitespace-pre-wrap" : "whitespace-pre"
  // In a narrow pane — a phone, a sheet — a row whose event word and lane
  // come before the message puts the message on a line of its own under
  // them, where it starts at the edge rather than past it; the lines of a
  // record under its head keep only their text.
  const stack = Boolean(eventColumn) || Boolean(showSource)
  const beside = stack && cont ? "@max-md:hidden" : undefined
  return (
    <div
      data-row-key={rowKey}
      tabIndex={rowKey === undefined ? undefined : -1}
      aria-current={open ? "true" : undefined}
      onClick={rowKey === undefined || !onPress ? undefined : () => onPress(rowKey)}
      className={cn(
        "relative flex items-start gap-x-3 py-px pr-4 pl-3.5 transition-colors [contain-intrinsic-size:auto_20px] [content-visibility:auto]",
        stack && "@max-md:flex-wrap @max-md:gap-x-2 @max-md:pl-2.5",
        onPress ? "cursor-pointer focus-ring-inset" : "cursor-default",
        loud && "bg-wash-danger",
        warned && "bg-wash-warning",
        // The opened line is a selection, and takes the fill every selection
        // in the product takes; hover stays the row's own.
        open ? "bg-accent" : "hover:bg-row-hover",
        line.context && "opacity-60",
      )}
    >
      <span
        aria-hidden
        className={cn(
          "absolute inset-y-0 left-0 w-0.5",
          line.level ? LEVEL_EDGE[line.level] : "bg-transparent",
        )}
      />
      {showLineNumbers && (
        <span className="numeric w-12 shrink-0 text-right text-muted-foreground/40 select-none @max-lg:hidden">
          {line.no ?? ""}
        </span>
      )}
      {showTime && (
        <span
          title={line.timestamp ? timestamp(line.timestamp) : undefined}
          className={cn(
            "numeric shrink-0 text-muted-foreground/70 select-none",
            TIME_WIDTH[time],
            beside,
          )}
        >
          {formatLogTime(line.timestamp, time, prev)}
        </span>
      )}
      {/* The level has a column of its own so a page of lines can be read
          down for the red ones, rather than each line being read across to
          find out. Where the lens named what the line records, its word takes
          the column — "deadlock" says more than "err" — and the level stays
          as the edge and the wash. Coloured, it is a word at the line's own
          size; a 10px tag beside 12px text was the quietest thing on the row. */}
      <span
        className={cn("flex shrink-0 select-none", !eventColumn && "w-10", beside)}
        style={eventColumn ? { width: `${eventColumn}ch` } : undefined}
      >
        {cont ? null : event ? (
          highlight ? (
            <span
              title={line.event}
              className={cn("truncate", EVENT_WORD[event.tone ?? "default"])}
            >
              {event.label}
            </span>
          ) : (
            <Tag tone={event.tone} className="truncate leading-[inherit]">
              {event.label}
            </Tag>
          )
        ) : (
          LEVEL_MARK[level] &&
          (highlight ? (
            <span className={cn("uppercase", LEVEL_WORD[level])}>{LEVEL_MARK[level]}</span>
          ) : (
            <Tag tone={LEVEL_TONE[level]} className="leading-[inherit]">
              {LEVEL_MARK[level]}
            </Tag>
          ))
        )}
      </span>
      {highlight &&
        columns?.map((key) => {
          const value = cont ? undefined : lineValue(line, key)
          return (
            <span
              key={key}
              className={cn(
                "hidden shrink-0 overflow-hidden @min-[900px]:flex",
                COLUMN_WIDTH[fieldOf(key).kind] ?? "w-16",
              )}
            >
              {value && <FieldValue name={key} value={value} compact lens={lens} />}
            </span>
          )
        })}
      {showFile && line.file && (
        <span
          className="w-28 shrink-0 truncate text-muted-foreground/70 @max-xl:hidden"
          title={line.file}
        >
          {line.file}
        </span>
      )}
      {showSource && line.source && (
        <span
          className={cn("max-w-40 shrink-0 truncate font-medium text-muted-foreground", beside)}
          style={highlight ? laneStyle(line.source) : undefined}
          title={line.source}
        >
          {line.source}
        </span>
      )}
      {line.stream === "stderr" && (
        // A mark, not a verdict: plenty of programs — Postgres among them —
        // write everything to stderr, and a red tag on each line said a
        // failure that was not there.
        <span className="shrink-0 text-muted-foreground/70 select-none">stderr</span>
      )}
      {repeat && repeat > 1 && (
        <span
          className="numeric shrink-0 font-medium text-muted-foreground select-none"
          title={`${repeat.toLocaleString()} identical lines${since ? ` since ${timestamp(since)}` : ""}`}
        >
          ×{repeat.toLocaleString()}
        </span>
      )}
      {highlight ? (
        <LogText
          text={shown}
          hits={ranges}
          // A file's own timestamp repeats the column beside it on every
          // line, which on syslog was a third of the width.
          skipTime={showTime && Boolean(line.timestamp) && !structured}
          hostname={hostname}
          className={cn("min-w-0 text-foreground", flow, stack && !cont && "@max-md:basis-full")}
        />
      ) : (
        <span
          className={cn(
            "min-w-0",
            flow,
            line.level && LEVEL_TEXT[line.level],
            stack && !cont && "@max-md:basis-full",
          )}
        >
          {segmentLine(line.text, ranges).map((part, k) =>
            part.hit ? (
              <mark key={k} className="rounded-sm bg-mark px-px text-foreground">
                {part.text}
              </mark>
            ) : (
              <span key={k}>{part.text}</span>
            ),
          )}
        </span>
      )}
    </div>
  )
})

/**
 * The time column's reading, as the Time toggle's menu. The accessible name
 * stays "Time", the name the switch it replaced had.
 */
function TimeMenu({ time }: { time: LogTime }) {
  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              size="sm"
              variant={time !== "off" ? "secondary" : "ghost"}
              className="h-7 shrink-0 gap-1.5 px-2 text-xs"
              aria-label="Time"
            >
              <Clock className="size-3" />
              <span className="hidden 2xl:inline">Time</span>
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>How the time each line was written is shown</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="min-w-44">
        <DropdownMenuLabel>Time</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={time}
          onValueChange={(value) => setLogView({ time: value as LogTime })}
        >
          {LOG_TIMES.map((mode) => (
            <DropdownMenuRadioItem key={mode.id} value={mode.id}>
              {mode.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * A view toggle on the strip above the lines. The word appears only on the
 * widest screens: beside six level chips there is no room for five words, and
 * the glyph, the tooltip and the accessible name carry it the rest of the way.
 */
function ToolbarToggle({
  active,
  onClick,
  icon: Icon,
  label,
  hint,
  extra,
}: {
  active?: boolean
  onClick: () => void
  icon: React.ComponentType<{ className?: string }>
  label: string
  hint: string
  /** A reading that stays visible at every width — the held count. */
  extra?: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="sm"
          variant={active ? "secondary" : "ghost"}
          className="h-7 shrink-0 gap-1.5 px-2 text-xs"
          aria-label={label}
          aria-pressed={active}
          onClick={onClick}
        >
          <Icon className="size-3" />
          <span className="hidden 2xl:inline">{label}</span>
          {extra}
          {/* In a narrow pane the ground says it is on, and the tick was
              width the chips beside it needed. */}
          {active && !extra && <Check className="size-3 2xl:hidden @max-lg:hidden" />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{hint}</TooltipContent>
    </Tooltip>
  )
}
