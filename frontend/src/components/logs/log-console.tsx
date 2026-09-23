"use client"

import { memo, useCallback, useLayoutEffect, useMemo, useRef, useState } from "react"
import {
  Backspace,
  BlendMode,
  Check,
  ChevronDoubleDown,
  CodeWrap,
  Copy,
  Pause,
  Play,
  TextFormat,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { clock, timestamp } from "@/lib/format"
import type { LogLine } from "@/lib/types"
import {
  LEVEL_EDGE,
  LEVEL_MARK,
  LEVEL_TEXT,
  LEVEL_TONE,
  highlightRanges,
  segmentLine,
  type LogLevel,
} from "@/lib/log-filter"
import { LEVEL_WORD, LogText, laneStyle, structuredText } from "@/components/logs/log-text"
import { setLogView, useLogView } from "@/lib/log-view"
import { useMetrics } from "@/hooks/use-metrics"
import type { LogFilterState } from "@/components/logs/types"
import { PaneFooter } from "@/components/panel"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { copyText } from "@/lib/clipboard"

function lineToText(line: LogLine, withTime: boolean) {
  const stamp = withTime && line.timestamp ? `${timestamp(line.timestamp)} ` : ""
  return `${stamp}${line.text}`
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
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [following, setFollowing] = useState(true)
  const [pinned, setPinned] = useState<number | null>(null)
  const { wrap, timestamps: showTime, highlight } = useLogView()
  const pin = useCallback((i: number) => setPinned((p) => (p === i ? null : i)), [])
  const hostname = useMetrics().host?.hostname

  const toBottom = useCallback(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [])

  useLayoutEffect(() => {
    if (following) toBottom()
  }, [lines, following, toBottom])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight < 40)
  }

  const copyAll = () =>
    copyText(
      lines.map((l) => lineToText(l, showTime)).join("\n"),
      `Copied ${lines.length.toLocaleString()} lines`,
    )

  // Ranges come from the server for a search — it can re-run its own regular
  // expression and the browser cannot — and are worked out here for a live
  // tail, where computing them per line on the streaming path would be work
  // for lines nobody scrolls back to.
  const needsClientHighlight = useMemo(
    () => filter.q !== "" && !lines.some((l) => l.match?.length),
    [filter.q, lines],
  )

  return (
    <div className={cn("flex min-h-0 min-w-0 flex-1 flex-col", className)}>
      {/* One row that scrolls sideways rather than wrapping: on a phone the
          chips are the thing to reach, and two more rows of chrome above the
          lines were two more rows of lines lost. */}
      <div className="flex min-h-9 shrink-0 items-center gap-1 overflow-x-auto border-b border-hairline px-2 py-1">
        {leading}
        <div className="min-w-2 flex-1" />
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
        <ToolbarToggle
          active={showTime}
          onClick={() => setLogView({ timestamps: !showTime })}
          icon={TextFormat}
          label="Time"
          hint="Show the timestamp this line was parsed out of"
        />
        <ToolbarToggle
          active={highlight}
          onClick={() => setLogView({ highlight: !highlight })}
          icon={BlendMode}
          label="Colour"
          hint="Colour each line by what is in it, and show structured lines as their message and fields. Off shows every line exactly as written."
        />
        <ToolbarToggle
          onClick={copyAll}
          icon={Copy}
          label="Copy"
          hint="Copy every line in this pane. Click one line to mark it, double-click to copy it."
        />
        {onClear && (
          <ToolbarToggle onClick={onClear} icon={Backspace} label="Clear" hint="Empty the pane" />
        )}
      </div>

      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-auto bg-surface-sunken font-mono text-xs leading-relaxed"
      >
        {lines.length === 0 ? (
          <div className="flex h-full items-center justify-center p-6">{empty}</div>
        ) : (
          // Keyed on arrival so the block rises once when the first lines
          // land and then holds still while the tail appends to it.
          <div key="lines" className={cn("animate-rise py-1.5", !wrap && "w-max min-w-full")}>
            {lines.map((line, i) => (
              <Line
                key={i}
                index={i}
                line={line}
                pinned={pinned === i}
                onPin={pin}
                showTime={showTime}
                showLineNumbers={showLineNumbers}
                showFile={showFile}
                showSource={showSource}
                wrap={wrap}
                highlight={highlight}
                hostname={hostname}
                filter={needsClientHighlight ? filter : undefined}
              />
            ))}
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
          <span className="numeric whitespace-nowrap">{lines.length.toLocaleString()} lines</span>
        </span>
        {footer}
      </PaneFooter>
    </div>
  )
}

/**
 * One line of the pane.
 *
 * Memoised on its own props because the live tail appends: without it every
 * arriving line redrew the four thousand above it, and a coloured line is a
 * dozen spans rather than one text node.
 *
 * Coloured (`highlight`), a line is its shapes: a level in its own colour in a
 * column of its own, an error or a warning washed across the row the way the
 * build console washes a failing step — found by scrolling, not by reading —
 * and a structured line drawn as its message and fields rather than as JSON.
 * Uncoloured it is exactly the text that was written, with the level's tint on
 * it as before.
 */
const Line = memo(function Line({
  index,
  line,
  pinned,
  onPin,
  showTime,
  showLineNumbers,
  showFile,
  showSource,
  wrap,
  highlight,
  hostname,
  filter,
}: {
  index: number
  line: LogLine
  pinned: boolean
  onPin: (index: number) => void
  showTime: boolean
  showLineNumbers?: boolean
  showFile?: boolean
  showSource?: boolean
  wrap: boolean
  highlight: boolean
  hostname?: string
  /** Set when the hits have to be found here rather than sent by the server. */
  filter?: LogFilterState
}) {
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
  return (
    <div
      onClick={() => onPin(index)}
      onDoubleClick={() => {
        void copyText(lineToText(line, showTime), "Line copied")
      }}
      className={cn(
        "flex cursor-default items-start gap-3 py-px pr-4 transition-colors [contain-intrinsic-size:auto_20px] [content-visibility:auto] hover:bg-row-hover",
        loud && "bg-wash-danger",
        warned && "bg-wash-warning",
        // The marked line is a selection, and takes the fill every
        // selection in the product takes.
        pinned && "bg-accent hover:bg-accent",
        line.context && "opacity-60",
      )}
    >
      <span
        aria-hidden
        className={cn(
          "w-0.5 shrink-0 self-stretch",
          line.level ? LEVEL_EDGE[line.level] : "bg-transparent",
        )}
      />
      {showLineNumbers && (
        <span className="numeric w-12 shrink-0 text-right text-muted-foreground/40 select-none">
          {line.no ?? ""}
        </span>
      )}
      {showTime && (
        <span
          title={line.timestamp ? timestamp(line.timestamp) : undefined}
          className="numeric w-16 shrink-0 text-muted-foreground/70 select-none"
        >
          {line.timestamp ? clock(line.timestamp) : "—"}
        </span>
      )}
      {/* The level has a column of its own so a page of lines can be read
          down for the red ones, rather than each line being read across to
          find out. Coloured, it is the level's word at the line's own size —
          a 10px tag beside 12px text was the quietest thing on the row. */}
      <span className="flex w-10 shrink-0 select-none">
        {LEVEL_MARK[level] &&
          (highlight ? (
            <span className={cn("uppercase", LEVEL_WORD[level])}>{LEVEL_MARK[level]}</span>
          ) : (
            <Tag tone={LEVEL_TONE[level]} className="leading-[inherit]">
              {LEVEL_MARK[level]}
            </Tag>
          ))}
      </span>
      {showFile && line.file && (
        <span className="w-28 shrink-0 truncate text-muted-foreground/70" title={line.file}>
          {line.file}
        </span>
      )}
      {showSource && line.source && (
        <span
          className="max-w-40 shrink-0 truncate font-medium text-muted-foreground"
          style={highlight ? laneStyle(line.source) : undefined}
          title={line.source}
        >
          {line.source}
        </span>
      )}
      {line.stream === "stderr" && (
        <Tag tone="danger" className="leading-[inherit]">
          stderr
        </Tag>
      )}
      {highlight ? (
        <LogText
          text={shown}
          hits={ranges}
          // A file's own timestamp repeats the column beside it on every
          // line, which on syslog was a third of the width.
          skipTime={showTime && Boolean(line.timestamp) && !structured}
          hostname={hostname}
          className={cn(
            "min-w-0 text-foreground",
            wrap ? "break-all whitespace-pre-wrap" : "whitespace-pre",
          )}
        />
      ) : (
        <span
          className={cn(
            "min-w-0",
            wrap ? "break-all whitespace-pre-wrap" : "whitespace-pre",
            line.level && LEVEL_TEXT[line.level],
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
          {active && !extra && <Check className="size-3 2xl:hidden" />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{hint}</TooltipContent>
    </Tooltip>
  )
}
