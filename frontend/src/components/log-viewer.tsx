"use client"

import { useEffect, useRef, useState } from "react"
import { ChevronDoubleDown, MagnifyingGlass, Pause, Play } from "@/components/icons"
import { Pane, PaneHeader } from "@/components/panel"
import { cn } from "@/lib/utils"
import { clock } from "@/lib/format"
import { fieldValue, structuredOf } from "@/lib/log-format"
import type { LogLine } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const LEVEL_CLASS: Record<string, string> = {
  critical: "text-destructive",
  error: "text-destructive",
  warn: "text-warning",
  info: "text-foreground",
  debug: "text-muted-foreground",
}

/**
 * A plain-text line's severity, guessed from its own words.
 *
 * The stream is deliberately not consulted: plenty of programs log everything
 * to stderr (Postgres included), and painting every one of those lines red
 * makes the pane unreadable — the colour stops meaning "error" and starts
 * meaning "log". Real failures still name themselves (`FATAL`, `ERROR`,
 * `Traceback`), so matching those keeps the signal without the wash.
 */
function textLevel(text: string): string | undefined {
  if (/\b(fatal|critical|emerg|alert|panic|exception|traceback|segfault|oom|error|failed|failure|denied|refused)\b/i.test(text))
    return "error"
  if (/\b(warn|warning|deprecated|retry|timeout|slow)\b/i.test(text)) return "warn"
  return undefined
}

/**
 * Terminal-style log pane. It follows the tail automatically but stops the
 * moment the reader scrolls up — nothing is more frustrating than losing the
 * line you were reading to an autoscroll.
 */
export function LogViewer({
  lines,
  className,
  emptyMessage = "Waiting for output…",
  onClear,
  toolbar,
  showTimestamps = true,
}: {
  lines: LogLine[]
  className?: string
  emptyMessage?: string
  onClear?: () => void
  toolbar?: React.ReactNode
  showTimestamps?: boolean
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [following, setFollowing] = useState(true)
  const [filter, setFilter] = useState("")
  // Formatting a structured line hides fields it judged redundant, and the one
  // time that judgement is wrong is the time you are debugging the logger
  // itself. Raw is always one click away.
  const [raw, setRaw] = useState(false)

  useEffect(() => {
    if (!following) return
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [lines, following])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    setFollowing(atBottom)
  }

  const needle = filter.toLowerCase()
  // Filtering matches the original text, not the formatted line: a field the
  // formatter dropped is still a thing somebody searches for.
  const visible = needle ? lines.filter((l) => l.text.toLowerCase().includes(needle)) : lines
  const anyStructured = !raw && visible.some((line) => structuredOf(line) !== null)

  return (
    // bg-surface-sunken rather than a flat black: this pane appears inside a
    // light palette too, where a black rectangle is a hole in the page rather
    // than a terminal.
    <Pane className={cn("bg-surface-sunken", className)}>
      <PaneHeader className="flex-wrap gap-2 px-2.5">
        <div className="relative min-w-40 flex-1">
          <MagnifyingGlass className="absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter these lines"
            className="h-7 pl-7 text-xs"
          />
        </div>
        {toolbar}
        <span className="numeric text-hint whitespace-nowrap text-muted-foreground">
          {visible.length} lines
        </span>
        <Button
          size="sm"
          variant={following ? "secondary" : "ghost"}
          className="h-7 gap-1.5 px-2 text-xs"
          onClick={() => {
            setFollowing((f) => !f)
            if (!following && scrollRef.current) {
              scrollRef.current.scrollTop = scrollRef.current.scrollHeight
            }
          }}
        >
          {following ? <Pause className="size-3" /> : <Play className="size-3" />}
          {following ? "Following" : "Paused"}
        </Button>
        {(anyStructured || raw) && (
          <Button
            size="sm"
            variant={raw ? "secondary" : "ghost"}
            className="h-7 px-2 text-xs"
            onClick={() => setRaw((r) => !r)}
            title={raw ? "Read JSON lines as messages" : "Show the lines exactly as they arrived"}
          >
            {raw ? "Formatted" : "Raw"}
          </Button>
        )}
        {onClear && (
          <Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={onClear}>
            Clear
          </Button>
        )}
      </PaneHeader>

      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-auto p-2 font-mono text-xs leading-relaxed"
      >
        {visible.length === 0 ? (
          <p className="px-2 py-1 text-muted-foreground">{emptyMessage}</p>
        ) : (
          visible.map((line, i) => {
            const structured = raw ? null : structuredOf(line)
            const level = line.level ?? structured?.level ?? textLevel(line.text)
            return (
              // One line, one row: a hairline between neighbours and a hover
              // wash, so a dense feed scans line by line instead of blurring
              // into a block.
              <div
                key={i}
                className="flex gap-3 rounded-sm border-b border-hairline/60 px-2 py-[3px] break-all whitespace-pre-wrap last:border-b-0 hover:bg-surface-header/60"
              >
                {showTimestamps && (
                  <span className="shrink-0 text-muted-foreground/60 select-none">
                    {line.timestamp ? clock(line.timestamp) : ""}
                  </span>
                )}
                {structured ? (
                  <span className="min-w-0 flex-1">
                    {structured.level && (
                      <span
                        className={cn(
                          "mr-2 uppercase select-none",
                          LEVEL_CLASS[structured.level] ?? "text-muted-foreground",
                        )}
                      >
                        {structured.level}
                      </span>
                    )}
                    <span className={cn(level && LEVEL_CLASS[level])}>{structured.message}</span>
                    {structured.fields.map(([key, value]) => (
                      <span key={key} className="ml-2 text-muted-foreground/70">
                        {key}=<span className="text-muted-foreground">{fieldValue(value)}</span>
                      </span>
                    ))}
                  </span>
                ) : (
                  <span className={cn("flex-1", level && LEVEL_CLASS[level])}>{line.text}</span>
                )}
              </div>
            )
          })
        )}
      </div>

      {!following && (
        <button
          className="flex items-center justify-center gap-1.5 border-t border-hairline bg-surface-header py-1.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
          onClick={() => {
            setFollowing(true)
            if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight
          }}
        >
          <ChevronDoubleDown className="size-3" />
          Jump to latest
        </button>
      )}
    </Pane>
  )
}
