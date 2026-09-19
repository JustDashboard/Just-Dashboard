"use client"

import { useCallback, useLayoutEffect, useRef, useState } from "react"
import { ChevronDoubleDown, Copy, Pause, Play } from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes, clock, timestamp } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import type { RequestEntry, RequestSummary } from "@/lib/types"
import {
  CLASS_EDGE,
  CLASS_TEXT,
  latency,
  latencyShare,
  methodEmphasis,
  requestKey,
  requestURI,
  statusClass,
} from "@/lib/requests"
import { PaneFooter } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * The requests, and the chrome that belongs to them.
 *
 * Drawn as log lines rather than as a data table, and that is the deliberate
 * choice: a request record is read the way a log is read — down the left edge
 * for the time, across for the one row that matters — and a nine-column
 * `<table>` at this density spends its width on cell padding and its
 * maintenance on breakpoint rules. The registry blocks that draw this as a
 * table also draw a filled colour chip per HTTP verb, which turns a column of
 * GETs into a column of rectangles competing with the status for attention.
 *
 * Two things carry over from the log console next door, because they were
 * learned there and the two panes must behave the same way:
 *
 * **Following stops the moment the reader scrolls**, and resumes at the bottom.
 * **Pausing holds what arrives rather than dropping it**, so reading a busy
 * deployment does not cost you the requests you read past.
 *
 * `content-visibility` rather than a virtualiser: the browser skips layout for
 * rows outside the viewport, and the scrollbar stays honest, the expanded row
 * keeps its real height, and the browser's own find still works.
 */
export function RequestConsole({
  entries,
  summary,
  latencyKnown,
  empty,
  status,
  footer,
  leading,
  paused,
  onPausedChange,
  held = 0,
  onFilterPath,
}: {
  entries: RequestEntry[]
  summary: RequestSummary
  latencyKnown: boolean
  empty: React.ReactNode
  /** The footer's first words: the stream's state, or the window's summary. */
  status?: React.ReactNode
  footer?: React.ReactNode
  /** The left of the strip above the rows: the status-family chips. */
  leading?: React.ReactNode
  paused?: boolean
  onPausedChange?: (paused: boolean) => void
  held?: number
  /** Narrowing to the path under the pointer, from the row itself. */
  onFilterPath?: (path: string) => void
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [following, setFollowing] = useState(true)
  const [open, setOpen] = useState<string | null>(null)

  const toTop = useCallback(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = 0
  }, [])

  // Newest first, so following means holding the *top* rather than the bottom.
  // A request log read from the bottom is a request log nobody reads.
  useLayoutEffect(() => {
    if (following) toTop()
  }, [entries, following, toTop])

  const onScroll = () => {
    const el = scrollRef.current
    if (el) setFollowing(el.scrollTop < 24)
  }

  const copyAll = () =>
    copyText(
      entries
        .map(
          (e) =>
            `${e.time} ${e.method} ${e.status} ${latency(e.durationMs)} ${requestURI(e)} ${e.remoteIp ?? ""}`,
        )
        .join("\n"),
      `Copied ${entries.length.toLocaleString()} requests`,
    )

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex min-h-9 shrink-0 items-center gap-1 overflow-x-auto border-b border-hairline px-2 py-1">
        {leading}
        <div className="min-w-2 flex-1" />
        {onPausedChange && (
          <ToolbarToggle
            active={paused}
            onClick={() => onPausedChange(!paused)}
            icon={paused ? Play : Pause}
            label={paused ? "Resume" : "Pause"}
            hint={paused ? "Append what arrived while paused" : "Hold new requests while you read"}
            extra={
              paused && held > 0 ? (
                <span className="numeric">{held.toLocaleString()} held</span>
              ) : undefined
            }
          />
        )}
        <ToolbarToggle
          onClick={copyAll}
          icon={Copy}
          label="Copy"
          hint="Copy every request in this pane"
        />
      </div>

      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-auto bg-surface-sunken font-mono text-xs leading-relaxed"
      >
        {entries.length === 0 ? (
          <div className="flex h-full items-center justify-center p-6">{empty}</div>
        ) : (
          <div key="rows" className="animate-rise py-1">
            {/*
              A header, because unlike a log line these columns are not
              self-describing: "512" is a size and "34ms" is a duration only
              once something says so. It is `text-hint` medium and opaque, so
              rows scroll under it rather than through it.
            */}
            <div className="sticky top-0 z-10 flex items-center gap-3 border-b border-hairline bg-surface-sunken px-0 pr-4 pb-1 text-hint font-medium text-muted-foreground">
              <span aria-hidden className="w-0.5 shrink-0" />
              <span className="w-16 shrink-0">Time</span>
              <span className="w-12 shrink-0">Method</span>
              <span className="w-9 shrink-0 text-right">Code</span>
              {latencyKnown && <span className="w-20 shrink-0 text-right">Took</span>}
              <span className="min-w-0 flex-1">Path</span>
              <span className="hidden w-16 shrink-0 text-right lg:block">Size</span>
              <span className="hidden w-32 shrink-0 truncate xl:block">Client</span>
            </div>

            {entries.map((entry, i) => {
              const key = requestKey(entry, i)
              const klass = statusClass(entry.status)
              const share = latencyShare(entry.durationMs, summary)
              return (
                <div key={key} className="[contain-intrinsic-size:auto_22px] [content-visibility:auto]">
                  <div
                    onClick={() => setOpen((current) => (current === key ? null : key))}
                    className={cn(
                      "flex cursor-default items-center gap-3 py-px pr-4 transition-colors hover:bg-row-hover",
                      open === key && "bg-accent hover:bg-accent",
                    )}
                  >
                    <span
                      aria-hidden
                      className={cn("w-0.5 shrink-0 self-stretch", CLASS_EDGE[klass])}
                    />
                    <span
                      title={timestamp(entry.time)}
                      className="numeric w-16 shrink-0 text-muted-foreground/70 select-none"
                    >
                      {clock(entry.time)}
                    </span>
                    <span className={cn("w-12 shrink-0 truncate", methodEmphasis(entry.method))}>
                      {entry.method}
                    </span>
                    <span className={cn("numeric w-9 shrink-0 text-right font-medium", CLASS_TEXT[klass])}>
                      {entry.status}
                    </span>
                    {latencyKnown && (
                      // The bar is behind the figure rather than beside it: a
                      // separate bar column costs 60px of path, and what the
                      // reader wants from it is "is this row one of the slow
                      // ones", which a wash answers at a glance.
                      <span className="relative w-20 shrink-0 overflow-hidden text-right">
                        {share > 0 && (
                          <span
                            aria-hidden
                            className={cn(
                              "absolute inset-y-px right-0 rounded-sm",
                              // The meter track is the product's "this is a
                              // proportion" ground; the danger wash is what a
                              // tinted band takes when it means something.
                              share > 0.75 ? "bg-wash-danger" : "bg-meter-track",
                            )}
                            style={{ width: `${Math.max(share * 100, 4)}%` }}
                          />
                        )}
                        <span className="numeric relative text-muted-foreground">
                          {latency(entry.durationMs)}
                        </span>
                      </span>
                    )}
                    <span className="min-w-0 flex-1 truncate" title={requestURI(entry)}>
                      {entry.path}
                      {entry.query && (
                        <span className="text-muted-foreground/60">?{entry.query}</span>
                      )}
                    </span>
                    <span className="numeric hidden w-16 shrink-0 text-right text-muted-foreground/70 lg:block">
                      {entry.size ? bytes(entry.size, 0) : "—"}
                    </span>
                    <span
                      className="numeric hidden w-32 shrink-0 truncate text-muted-foreground/70 xl:block"
                      title={entry.userAgent}
                    >
                      {entry.remoteIp ?? "—"}
                    </span>
                  </div>

                  {open === key && (
                    <RequestDetail entry={entry} onFilterPath={onFilterPath} />
                  )}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {!following && entries.length > 0 && (
        <button
          className="flex items-center justify-center gap-1.5 border-t border-hairline py-1.5 text-xs text-muted-foreground focus-ring-inset transition-colors hover:bg-row-hover hover:text-foreground"
          onClick={() => {
            setFollowing(true)
            toTop()
          }}
        >
          <ChevronDoubleDown className="size-3 rotate-180" />
          Jump to the newest
          {held > 0 && <span className="numeric">· {held.toLocaleString()} held</span>}
        </button>
      )}

      <PaneFooter className="gap-x-4 gap-y-1 px-3 text-hint text-muted-foreground">
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          {status}
          <span className="numeric whitespace-nowrap">
            {entries.length.toLocaleString()} shown
          </span>
        </span>
        {footer}
      </PaneFooter>
    </div>
  )
}

/**
 * One request, opened.
 *
 * Everything the row had no width for. It is a disclosure inside the row
 * rather than a drawer over the page, because the question it answers — "what
 * was this one" — is asked while scanning, and a panel that covers the list
 * makes the reader close it to ask the same question about the next row.
 */
function RequestDetail({
  entry,
  onFilterPath,
}: {
  entry: RequestEntry
  onFilterPath?: (path: string) => void
}) {
  const facts: [string, React.ReactNode][] = [
    ["When", timestamp(entry.time)],
    ["Request", `${entry.method} ${requestURI(entry)}`],
    ["Status", `${entry.status}`],
    ["Took", latency(entry.durationMs)],
    ["Sent", entry.size ? bytes(entry.size) : "nothing"],
    ["Host", entry.host ?? "—"],
    ["Protocol", [entry.proto, entry.tls ? "TLS" : "plaintext"].filter(Boolean).join(" · ")],
    ["Client", entry.remoteIp ?? "—"],
    ["Agent", entry.userAgent || "—"],
    ["Referer", entry.referer || "—"],
  ]
  return (
    <div className="animate-rise border-y border-hairline bg-background px-4 py-3 pl-[1.125rem]">
      <dl className="grid grid-cols-1 gap-x-6 gap-y-1.5 sm:grid-cols-2 xl:grid-cols-3">
        {facts.map(([label, value]) => (
          <div key={label} className="flex min-w-0 gap-2">
            <dt className="w-20 shrink-0 font-sans text-hint text-muted-foreground">{label}</dt>
            <dd className="min-w-0 truncate" title={typeof value === "string" ? value : undefined}>
              {value}
            </dd>
          </div>
        ))}
      </dl>
      <div className="mt-2.5 flex flex-wrap gap-2">
        {onFilterPath && (
          <Button
            size="sm"
            variant="secondary"
            className="h-7 font-sans text-xs"
            onClick={() => onFilterPath(entry.path)}
          >
            Show every request to this path
          </Button>
        )}
        <Button
          size="sm"
          variant="ghost"
          className="h-7 font-sans text-xs"
          onClick={() => void copyText(JSON.stringify(entry, null, 2), "Request copied")}
        >
          Copy as JSON
        </Button>
      </div>
    </div>
  )
}

/** A control on the strip above the rows, matching the log console's own. */
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
        </Button>
      </TooltipTrigger>
      <TooltipContent>{hint}</TooltipContent>
    </Tooltip>
  )
}
