"use client"

import { get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import type { NetworkRouteEvent, NetworkRouteHistory } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Cross, Route as RouteGlyph } from "@/components/icons"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FilterChip } from "@/components/tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { cn } from "@/lib/utils"

const CHANGE: Record<
  NetworkRouteEvent["change"],
  { tone: "running" | "danger" | "warning"; label: string }
> = {
  added: { tone: "running", label: "Added" },
  removed: { tone: "danger", label: "Removed" },
  changed: { tone: "warning", label: "Changed" },
}

/**
 * What the route observer saw change, newest first: a line a change, what it
 * was in mono, the readings it fell between at the line's end. A change
 * across a restart says so. What periodic readings cannot see was a footnote
 * under the list; it is behind the ⓘ on the title line now, with the
 * observer's cadence beside it.
 */
export function RouteHistoryPanel({
  target,
  onClearTarget,
}: {
  /** The address or network the page is asking about: only changes touching it. */
  target?: string
  onClearTarget: () => void
}) {
  const history = usePoll<NetworkRouteHistory>(
    (signal) => get("/network/routing/history", target ? { target } : undefined, signal),
    30_000,
    [target],
  )
  const data = history.data
  return (
    <Panel plain aria-label="Route history">
      <PanelHeader
        title="Route history"
        actions={
          <>
            {target && (
              <FilterChip selected aria-label="Show every change" onClick={onClearTarget}>
                <span className="font-mono">{target}</span>
                <Cross aria-hidden className="size-3" />
              </FilterChip>
            )}
            {data?.lastError && (
              <span className="flex items-center gap-1.5">
                <Status tone="warning" label="Last reading failed" />
                <InfoTip label="Why the last reading failed">{data.lastError}</InfoTip>
              </span>
            )}
            {data && (
              <span className="numeric text-hint text-muted-foreground">
                {data.running
                  ? `read every ${data.intervalSeconds}s${data.lastReading ? ` · last ${relativeTime(data.lastReading)}` : ""}`
                  : "observer not running"}
              </span>
            )}
            {data && data.limits.length > 0 && (
              <InfoTip label="What the history cannot see">
                <span className="block space-y-1.5 text-left">
                  {data.limits.map((limit) => (
                    <span key={limit} className="block">
                      {limit}
                    </span>
                  ))}
                </span>
              </InfoTip>
            )}
          </>
        }
      />
      <PanelBody className="space-y-3">
        {data && (
          <NetworkReadWarning
            error={history.error}
            refresh={history.refresh}
            lastSuccess={history.lastSuccess}
            reading="route history"
          />
        )}
        {!data && history.error ? (
          <ErrorState error={history.error} onRetry={history.refresh} />
        ) : !data ? (
          <ul aria-busy className="divide-y divide-hairline">
            {Array.from({ length: 3 }, (_, i) => (
              <li key={i} className="flex h-14 items-center gap-4">
                <Skeleton className="h-3 w-16" />
                <Skeleton className="h-3 w-72 max-w-[50%]" />
                <Skeleton className="ml-auto h-3 w-20" />
              </li>
            ))}
          </ul>
        ) : data.events.length === 0 ? (
          <EmptyState
            icon={RouteGlyph}
            title={target ? `No recorded change touches ${target}` : "Nothing has changed"}
            description={
              target
                ? undefined
                : data.running
                  ? "No route or rule has changed since the observer started reading."
                  : "The route observer is not running, so changes are not being recorded."
            }
          />
        ) : (
          <ol className="divide-y divide-hairline">
            {data.events.map((event) => (
              <Change key={event.id} event={event} />
            ))}
          </ol>
        )}
      </PanelBody>
    </Panel>
  )
}

function Change({ event }: { event: NetworkRouteEvent }) {
  const change = CHANGE[event.change]
  return (
    <li
      data-slot="route-change"
      className="grid min-h-14 min-w-0 grid-cols-[5.5rem_minmax(0,1fr)] items-center gap-x-3 gap-y-1 py-2.5 sm:grid-cols-[5.5rem_minmax(0,1fr)_auto]"
    >
      <Status tone={change.tone} label={change.label} />
      <div className="min-w-0 space-y-1">
        <p className="font-mono text-xs break-all">
          {event.before && (
            <span
              className={cn(
                "text-muted-foreground",
                (event.change === "removed" || event.after) &&
                  "line-through decoration-muted-foreground/60",
              )}
            >
              {event.before}
            </span>
          )}
          {event.before && event.after && <span className="text-muted-foreground"> → </span>}
          {event.after && <span>{event.after}</span>}
        </p>
        <p className="flex min-w-0 flex-wrap items-center gap-x-2 text-hint text-muted-foreground">
          <span>
            {event.object}
            {event.table !== undefined && event.object === "route"
              ? ` · ${event.tableName || `table ${event.table}`}`
              : ""}
            {event.family === "inet6" ? " · IPv6" : ""}
          </span>
          <span className={event.managed ? "text-brand" : undefined}>
            {event.managed ? "made here" : event.owner}
          </span>
          <span className="sm:hidden">
            · <When event={event} />
          </span>
        </p>
      </div>
      <p className="text-right text-hint text-muted-foreground max-sm:hidden">
        <When event={event} />
      </p>
    </li>
  )
}

/** When it was seen, and the reading before it: a change is only known to lie between the two. */
function When({ event }: { event: NetworkRouteEvent }) {
  return (
    <>
      <time dateTime={event.observedAt} title={timestamp(event.observedAt)}>
        {relativeTime(event.observedAt)}
      </time>
      <span className="sm:block">
        {" "}
        {event.acrossRestart ? "while not reading" : `after ${timestamp(event.previousAt)}`}
      </span>
    </>
  )
}
