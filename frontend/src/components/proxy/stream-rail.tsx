"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { Connection } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { StreamEntry } from "@/lib/types"
import { stateLabel, stateStatus } from "@/lib/streams"
import { SearchInput } from "@/components/page"
import { ProductLogo, portProduct } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { LoadingRows } from "@/components/state"
import { routeLine, streamHref, type StreamTab } from "@/components/proxy/stream-facts"

function dotTone(stream: StreamEntry): DotTone {
  const status = stateStatus(stream)
  if (status.tone) return status.tone
  return status.verdict === "ok" ? "running" : status.verdict === "critical" ? "danger" : "warning"
}

/**
 * The Streams list, folded into a column beside the stream it opened: the
 * list's own search and chip, in its order (worst first), so the next stream
 * to check is one press away and the view on screen goes with it. Each row is
 * the port as the service it is with its state in the tile's corner, the
 * stream's name, and where it goes — or, for one that is not live, why. The
 * one on screen is filled.
 */
export function StreamRail({
  streams,
  current,
  tab,
  loading,
  query,
  onQuery,
  filter,
  onClearFilter,
  caption,
  className,
}: {
  streams: StreamEntry[]
  current: string
  /** The view on screen, carried to the next stream. */
  tab: StreamTab
  loading: boolean
  query: string
  onQuery: (value: string) => void
  /** The list's chip, when one other than All is chosen. */
  filter?: string
  onClearFilter: () => void
  caption: string
  className?: string
}) {
  const list = useRef<HTMLOListElement>(null)
  // The stream on screen is kept in view: opened by Alt+↓ past the column's
  // edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current])

  return (
    <nav
      aria-label="Streams"
      data-slot="stream-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Name, port, upstream or source"
          aria-label="Search the streams"
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{caption}</span>
          {filter && (
            <button
              type="button"
              onClick={onClearFilter}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filter
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label="Streams to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {streams.map((stream) => {
          const here = stream.name === current
          const live = !stream.error && stream.state === "live"
          return (
            <li key={stream.name} className="min-w-0">
              <Link
                href={streamHref(stream.name, tab)}
                aria-current={here ? "page" : undefined}
                aria-label={stream.name}
                className={cn(
                  "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
                  here ? "bg-accent" : "hover:bg-row-hover",
                )}
              >
                <span className="relative flex shrink-0">
                  <ProductLogo
                    id={portProduct(stream.listen)}
                    size="sm"
                    fallback={Connection}
                    className="size-7 rounded-md [&_img]:size-4"
                  />
                  <span
                    aria-hidden
                    className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
                  >
                    <StatusDot tone={dotTone(stream)} className="size-1.5" />
                  </span>
                </span>
                <span className="min-w-0 flex-1">
                  <span
                    className={cn(
                      "block truncate text-body",
                      here ? "font-semibold" : "font-medium",
                    )}
                  >
                    {stream.name}
                  </span>
                  <span
                    className={cn(
                      "block truncate text-hint",
                      live || stream.paused ? "font-mono text-muted-foreground" : "text-warning",
                      (stream.error || stream.state === "not-listening") && "text-destructive",
                    )}
                  >
                    {stream.error
                      ? "could not be read"
                      : live || stream.paused
                        ? routeLine(stream)
                        : stateLabel(stream.state)}
                  </span>
                </span>
              </Link>
            </li>
          )
        })}
        {loading && streams.length === 0 && <LoadingRows rows={6} />}
        {!loading && streams.length === 0 && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No stream matches.</li>
        )}
      </ol>
    </nav>
  )
}
