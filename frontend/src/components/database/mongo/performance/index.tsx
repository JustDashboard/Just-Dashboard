"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { bytes, percent } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { utilisationTone } from "@/components/meter"
import { TileTrend } from "@/components/metrics/sparkline"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { tabClasses } from "@/components/tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { SectionError, SectionFrame } from "@/components/database/kit"
import { mongoDatabases, mongoServer } from "@/components/database/mongo/api"
import { OperationsView } from "@/components/database/mongo/performance/operations"
import { OverviewView } from "@/components/database/mongo/performance/overview"
import { ProfilerView } from "@/components/database/mongo/performance/profiler"
import { ReplicationView } from "@/components/database/mongo/performance/replication"
import {
  OPERATIONS,
  addSample,
  latest,
  lifetimeTargeting,
  ratio,
  seriesOf,
  statRows,
} from "@/components/database/mongo/performance/samples"
import type { MongoServer } from "@/components/database/mongo/types"
import { useMongo } from "@/components/database/mongo/use-mongo"
import { micros, perSecond } from "@/components/database/redis/performance/samples"
import { worthRetrying } from "@/components/database/redis/read-error"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"

/** How often the counters are read: one `serverStatus` each time. */
const EVERY_MS = 3000

/** Documents examined for each returned, past which a query is doing work it should not. */
const POOR_TARGETING = 100

type View = "overview" | "operations" | "slow" | "replication"

/**
 * What the server is doing: how busy it is, who is connected, what its cache
 * holds, and what has been slow.
 *
 * Five readings head the page — operations a second, connections against the
 * limit, the cache against its ceiling, how many documents a query examines
 * for each it returns, and how long a read takes — each with the shape it has
 * had since the page was opened. They come from the server's counters,
 * sampled every few seconds; a rate is the difference between two samples,
 * so the first line of a trend appears with the second sample and nothing
 * here is recorded history.
 *
 * Under them the page reads four ways: the same samples as charts; the
 * operations in progress, each of which can be stopped; the slow operations
 * the profiler kept; and, on a replica set, its members.
 */
export function MongoPerformance() {
  const mongo = useMongo()
  const { id, engine, param, select } = mongo
  const { confirm, dialog } = useConfirm()
  useFocusReturn()
  const stats = usePoll((signal) => mongoServer(id, signal), EVERY_MS, [id])
  const databases = usePoll((signal) => mongoDatabases(id, signal), 120_000, [id])
  const [samples, setSamples] = useState<MongoServer[]>([])
  const newest = samples[samples.length - 1]
  if (stats.data && (!newest || stats.data.timestamp !== newest.timestamp)) {
    const next = addSample(samples, stats.data)
    if (next !== samples) setSamples(next)
  }
  const rows = useMemo(() => statRows(samples), [samples])

  const replicaSet = newest?.topology === "replicaset"
  const views: { id: View; label: string }[] = [
    { id: "overview", label: "Overview" },
    ...(engine.can("sessions") ? [{ id: "operations" as const, label: "Operations" }] : []),
    ...(engine.can("profiler") ? [{ id: "slow" as const, label: "Slow operations" }] : []),
    ...(engine.can("replication") && replicaSet
      ? [{ id: "replication" as const, label: "Replica set" }]
      : []),
  ]
  const asked = param("view")
  const view: View = views.some((entry) => entry.id === asked) ? (asked as View) : "overview"

  // On a phone the strip is wider than the page and scrolls sideways: the
  // view on screen is kept in sight in it.
  const strip = useRef<HTMLDivElement>(null)
  useEffect(() => {
    strip.current
      ?.querySelector('[aria-pressed="true"]')
      ?.scrollIntoView({ block: "nearest", inline: "nearest" })
  }, [view])

  if (stats.error && samples.length === 0) {
    return (
      <SectionError
        section="performance"
        error={worthRetrying(stats.error)}
        onRetry={stats.refresh}
      />
    )
  }

  const now = newest
  const ops = latest(rows, "ops")
  const total = now ? OPERATIONS.reduce((sum, kind) => sum + (now.opcounters[kind] ?? 0), 0) : 0
  const limit = now ? now.connections.current + now.connections.available : 0
  const connectionFill = now && limit > 0 ? (now.connections.current / limit) * 100 : undefined
  const cache = now?.cache
  const cacheFill = cache && cache.maxBytes > 0 ? (cache.bytes / cache.maxBytes) * 100 : undefined
  const targeting = latest(rows, "targeting") ?? (now ? lifetimeTargeting(now) : null)
  const targetingLive = latest(rows, "targeting") !== null
  const readLatency = latest(rows, "readLatency")
  const writeLatency = latest(rows, "writeLatency")

  return (
    <SectionFrame section="performance">
      {dialog}
      {!now ? (
        <StatGrid columns={5} dense aria-hidden>
          {Array.from({ length: 5 }, (_, i) => (
            <div key={i} className="space-y-2.5 px-5 py-4">
              <Skeleton className="h-2.5 w-16" />
              <Skeleton className="h-7 w-24" />
              <Skeleton className="h-3 w-32" />
            </div>
          ))}
        </StatGrid>
      ) : (
        <StatGrid columns={5} dense key="readings" className="animate-rise">
          <StatTile
            label="Operations a second"
            value={ops === null ? "—" : perSecond(ops)}
            trend={
              <TileTrend
                values={seriesOf(rows, "ops")}
                label="Operations a second"
                color="var(--chart-1)"
              />
            }
            hint={`${total.toLocaleString("en-US")} since it started`}
          />
          <StatTile
            label="Connections"
            value={now.connections.current.toLocaleString("en-US")}
            meter={connectionFill}
            tone={connectionFill !== undefined ? utilisationTone(connectionFill) : "default"}
            hint={`of ${limit.toLocaleString("en-US")} allowed · ${now.connections.active.toLocaleString("en-US")} doing work`}
          />
          <StatTile
            label="Cache"
            value={cacheFill === undefined ? "—" : percent(cacheFill)}
            meter={cacheFill}
            tone={cacheFill !== undefined ? utilisationTone(cacheFill) : "default"}
            hint={
              cache
                ? `${bytes(cache.bytes)} of ${bytes(cache.maxBytes)} · ${bytes(cache.dirtyBytes)} not yet written`
                : "This storage engine reports no cache"
            }
          />
          <StatTile
            label="Query targeting"
            value={targeting === null ? "—" : ratio(targeting)}
            tone={targeting !== null && targeting >= POOR_TARGETING ? "warning" : "default"}
            trend={
              <TileTrend
                values={seriesOf(rows, "targeting")}
                label="Documents examined for each returned"
                color="var(--chart-3)"
              />
            }
            hint={
              targeting === null
                ? "No query has returned a document yet"
                : targetingLive
                  ? "examined for each returned, just now"
                  : "examined for each returned, since it started"
            }
          />
          <StatTile
            label="A read takes"
            value={readLatency === null ? "—" : micros(readLatency)}
            trend={
              <TileTrend
                values={seriesOf(rows, "readLatency")}
                label="Time a read takes"
                color="var(--chart-4)"
              />
            }
            hint={
              readLatency === null
                ? "No read between the last two readings"
                : writeLatency === null
                  ? "on average, over the last reading"
                  : `a write ${micros(writeLatency)}`
            }
          />
        </StatGrid>
      )}

      <div className="min-w-0 space-y-6">
        {/* A strip of pressed buttons, not a landmark: these are readings of
            one page, and the rail is where the product navigates. The line
            under it is where a failed reading is said, so the page does not
            move when one fails. */}
        <div className="relative">
          <div
            ref={strip}
            role="group"
            aria-label="Performance views"
            className="flex gap-1 overflow-x-auto border-b border-hairline"
          >
            {views.map((entry) => (
              <button
                key={entry.id}
                type="button"
                aria-pressed={view === entry.id}
                onClick={() => select({ view: entry.id === "overview" ? null : entry.id })}
                className={tabClasses(view === entry.id, "h-10")}
              >
                {entry.label}
              </button>
            ))}
          </div>
          {stats.error && (
            <p
              role="status"
              className="absolute inset-x-0 top-full truncate pt-1 text-hint text-warning"
            >
              The last reading failed, so the figures are a few seconds old: {stats.error.message}
            </p>
          )}
        </div>
        {view === "operations" ? (
          <OperationsView mongo={mongo} confirm={confirm} />
        ) : view === "slow" ? (
          <ProfilerView mongo={mongo} databases={databases} />
        ) : view === "replication" ? (
          <ReplicationView mongo={mongo} />
        ) : (
          <OverviewView
            mongo={mongo}
            rows={rows}
            server={now}
            databases={databases}
            everyMs={EVERY_MS}
          />
        )}
      </div>
    </SectionFrame>
  )
}
