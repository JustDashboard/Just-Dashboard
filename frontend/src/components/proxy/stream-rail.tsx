"use client"

import { Connection } from "@/components/icons"
import type { StreamEntry } from "@/lib/types"
import { stateLabel, stateStatus } from "@/lib/streams"
import { ProductLogo, portProduct } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { Rail, RailLine, RailName, RailRow } from "@/components/rail"
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
  error,
  onRetry,
  query,
  onQuery,
  filter,
  onClearFilter,
  caption,
  className,
}: {
  streams: StreamEntry[]
  current: string
  /** The view carried to the next stream: the one on screen, except Settings. */
  tab: StreamTab
  loading: boolean
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  /** The list's chip, when one other than All is chosen. */
  filter?: string
  onClearFilter: () => void
  caption: string
  className?: string
}) {
  return (
    <Rail
      slot="stream-rail"
      label="Streams"
      search={{ placeholder: "Name, port, upstream or source", query, onQuery }}
      rows={streams}
      keyOf={(stream) => stream.name}
      current={(stream) => stream.name === current}
      row={(stream, here) => {
        const live = !stream.error && stream.state === "live"
        return (
          <RailRow
            item={stream.name}
            name={stream.name}
            current={here}
            href={streamHref(stream.name, tab)}
            mark={
              <ProductLogo
                id={portProduct(stream.listen)}
                size="sm"
                fallback={Connection}
                className="size-7 rounded-md [&_img]:size-4"
              />
            }
            dot={<StatusDot tone={dotTone(stream)} className="size-1.5" />}
          >
            <RailName current={here}>{stream.name}</RailName>
            <RailLine
              ink={
                stream.error || stream.state === "not-listening"
                  ? "text-destructive"
                  : live || stream.paused
                    ? undefined
                    : "text-warning"
              }
              className={live || stream.paused ? "font-mono" : undefined}
            >
              {stream.error
                ? "could not be read"
                : live || stream.paused
                  ? routeLine(stream)
                  : stateLabel(stream.state)}
            </RailLine>
          </RailRow>
        )
      }}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty="No stream matches."
      filtered={Boolean(filter)}
      onClearFilters={onClearFilter}
      caption={caption}
      className={className}
    />
  )
}
