"use client"

import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react"
import {
  Backspace,
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
import { LEVEL_EDGE, LEVEL_TEXT, highlightRanges, segmentLine } from "@/lib/log-filter"
import { setLogView, useLogView } from "@/lib/log-view"
import type { LogFilterState } from "@/components/logs/types"
import { Pane, PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { copyText } from "@/lib/clipboard"

function lineToText(line: LogLine, withTime: boolean) {
  const stamp = withTime && line.timestamp ? `${timestamp(line.timestamp)} ` : ""
  return `${stamp}${line.text}`
}

/**
 * The pane the logs page is mostly made of.
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
  status,
  actions,
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
  status?: React.ReactNode
  actions?: React.ReactNode
  empty: React.ReactNode
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
  const { wrap, timestamps: showTime } = useLogView()

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
    <Pane className={cn("bg-surface-sunken", className)}>
      <PaneHeader className="flex-wrap gap-x-2 gap-y-1.5 px-2.5">
        {status}
        <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
          {lines.length.toLocaleString()} lines
        </span>
        <div className="flex-1" />
        {actions}
        {onPausedChange && (
          <Button
            size="sm"
            variant={paused ? "secondary" : "ghost"}
            className="h-7 gap-1.5 px-2 text-xs"
            onClick={() => onPausedChange(!paused)}
            title={paused ? "Append what arrived while paused" : "Hold new lines while you read"}
          >
            {paused ? <Play className="size-3" /> : <Pause className="size-3" />}
            {paused ? (held > 0 ? `Resume · ${held.toLocaleString()} held` : "Resume") : "Pause"}
          </Button>
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
          onClick={copyAll}
          icon={Copy}
          label="Copy"
          hint="Copy every line in this pane. Click one line to mark it, double-click to copy it."
        />
        {onClear && (
          <ToolbarToggle onClick={onClear} icon={Backspace} label="Clear" hint="Empty the pane" />
        )}
      </PaneHeader>

      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-auto font-mono text-xs leading-[1.55]"
      >
        {lines.length === 0 ? (
          <div className="flex h-full items-center justify-center p-6">{empty}</div>
        ) : (
          <div className={cn("py-1", !wrap && "w-max min-w-full")}>
            {lines.map((line, i) => {
              const ranges = line.match?.length
                ? line.match
                : needsClientHighlight
                  ? highlightRanges(line.text, filter)
                  : undefined
              return (
                <div
                  key={i}
                  onClick={() => setPinned((p) => (p === i ? null : i))}
                  onDoubleClick={() => {
                    void copyText(lineToText(line, showTime), "Line copied")
                  }}
                  className={cn(
                    "flex cursor-default items-stretch gap-2.5 pr-3 transition-colors [contain-intrinsic-size:auto_20px] [content-visibility:auto] hover:bg-foreground/[0.04]",
                    pinned === i && "bg-plot-primary hover:bg-plot-primary",
                    line.context && "opacity-60",
                  )}
                >
                  <span
                    aria-hidden
                    className={cn(
                      "w-[3px] shrink-0",
                      line.level ? LEVEL_EDGE[line.level] : "bg-transparent",
                    )}
                  />
                  {showLineNumbers && (
                    <span className="numeric w-14 shrink-0 text-right text-muted-foreground/50 select-none">
                      {line.no ?? ""}
                    </span>
                  )}
                  {showTime && (
                    <span
                      title={line.timestamp ? timestamp(line.timestamp) : undefined}
                      className="w-[4.5rem] shrink-0 text-muted-foreground/60 select-none"
                    >
                      {line.timestamp ? clock(line.timestamp) : "—"}
                    </span>
                  )}
                  {showFile && line.file && (
                    <span
                      className="w-28 shrink-0 truncate text-muted-foreground/70"
                      title={line.file}
                    >
                      {line.file}
                    </span>
                  )}
                  {showSource && line.source && (
                    <span
                      className="max-w-40 shrink-0 truncate text-primary/75"
                      title={line.source}
                    >
                      {line.source}
                    </span>
                  )}
                  {line.stream === "stderr" && (
                    <span className="shrink-0 text-destructive/70 select-none">err</span>
                  )}
                  <span
                    className={cn(
                      "min-w-0",
                      wrap ? "break-all whitespace-pre-wrap" : "whitespace-pre",
                      line.level && LEVEL_TEXT[line.level],
                    )}
                  >
                    {segmentLine(line.text, ranges).map((part, k) =>
                      part.hit ? (
                        <mark key={k} className="rounded-[2px] bg-mark px-px text-foreground">
                          {part.text}
                        </mark>
                      ) : (
                        <span key={k}>{part.text}</span>
                      ),
                    )}
                  </span>
                </div>
              )
            })}
          </div>
        )}
      </div>

      {footer}

      {!following && lines.length > 0 && (
        <button
          className="flex items-center justify-center gap-1.5 border-t border-hairline bg-surface-header py-1.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
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
    </Pane>
  )
}

function ToolbarToggle({
  active,
  onClick,
  icon: Icon,
  label,
  hint,
}: {
  active?: boolean
  onClick: () => void
  icon: React.ComponentType<{ className?: string }>
  label: string
  hint: string
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="sm"
          variant={active ? "secondary" : "ghost"}
          className="h-7 gap-1.5 px-2 text-xs"
          onClick={onClick}
        >
          <Icon className="size-3" />
          <span className="hidden sm:inline">{label}</span>
          {active && <Check className="size-3 sm:hidden" />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{hint}</TooltipContent>
    </Tooltip>
  )
}
