"use client"

import { get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import type { NetworkRouteHistory } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { cn } from "@/lib/utils"

/**
 * What the route observer saw change, newest first. Each change is bounded
 * by the two readings it fell between; a change across a restart says so,
 * and what periodic readings cannot see is stated under the list rather than
 * implied by its absence.
 */
export function RouteHistoryPanel({ target }: { target?: string }) {
  const history = usePoll<NetworkRouteHistory>(
    (signal) => get("/network/routing/history", target ? { target } : undefined, signal),
    30_000,
    [target],
  )
  const data = history.data
  return (
    <Panel>
      <PanelHeader
        title="Route history"
        actions={
          data && (
            <span className="numeric text-hint text-muted-foreground">
              {data.running
                ? `read every ${data.intervalSeconds}s${data.lastReading ? ` · last ${relativeTime(data.lastReading)}` : ""}`
                : "observer not running"}
            </span>
          )
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
          <LoadingRows rows={3} />
        ) : data.events.length === 0 ? (
          <EmptyNote>
            {target
              ? `No recorded change touches ${target}.`
              : "No route or rule has changed since the observer started reading."}
          </EmptyNote>
        ) : (
          <ol className="divide-y divide-hairline rounded-lg border border-hairline">
            {data.events.map((event) => (
              <li
                key={event.id}
                className="grid min-w-0 gap-1 px-3 py-2.5 sm:grid-cols-[9rem_minmax(0,1fr)]"
              >
                <div className="text-hint text-muted-foreground">
                  <time dateTime={event.observedAt} title={timestamp(event.observedAt)}>
                    {relativeTime(event.observedAt)}
                  </time>
                  <span className="block">
                    {event.acrossRestart
                      ? "while not reading"
                      : `after ${timestamp(event.previousAt)}`}
                  </span>
                </div>
                <div className="min-w-0 space-y-1">
                  <span className="flex flex-wrap items-center gap-2">
                    <Tag
                      tone={
                        event.change === "removed"
                          ? "danger"
                          : event.change === "added"
                            ? "success"
                            : "warning"
                      }
                    >
                      {event.change}
                    </Tag>
                    <span className="text-xs">
                      {event.object}
                      {event.table !== undefined && event.object === "route"
                        ? ` · ${event.tableName || `table ${event.table}`}`
                        : ""}
                      {event.family === "inet6" ? " · IPv6" : ""}
                    </span>
                    <span
                      className={cn(
                        "text-xs",
                        event.managed ? "text-brand" : "text-muted-foreground",
                      )}
                    >
                      {event.managed ? "made here" : event.owner}
                    </span>
                  </span>
                  {event.before && (
                    <code className="block font-mono text-xs break-all text-muted-foreground line-through decoration-muted-foreground/60">
                      {event.before}
                    </code>
                  )}
                  {event.after && (
                    <code className="block font-mono text-xs break-all">{event.after}</code>
                  )}
                </div>
              </li>
            ))}
          </ol>
        )}
        {data && (
          <ul className="space-y-1 text-hint text-muted-foreground">
            {data.lastError && (
              <li className="text-warning">Last reading failed: {data.lastError}</li>
            )}
            {data.limits.map((limit) => (
              <li key={limit}>{limit}</li>
            ))}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}
