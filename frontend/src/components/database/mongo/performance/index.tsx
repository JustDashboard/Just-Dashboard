"use client"

import { useConfirm } from "@/components/confirm-dialog"
import { BlockedState, SectionError, SectionFrame } from "@/components/database/kit"
import { mongoDatabases, mongoServer } from "@/components/database/mongo/api"
import { OperationsView } from "@/components/database/mongo/performance/operations"
import { OverviewView } from "@/components/database/mongo/performance/overview"
import { ProfilerView } from "@/components/database/mongo/performance/profiler"
import { ReplicationView } from "@/components/database/mongo/performance/replication"
import {
  addSample,
  reportsNothing,
  statRows,
} from "@/components/database/mongo/performance/samples"
import type { MongoServer } from "@/components/database/mongo/types"
import { useMongo } from "@/components/database/mongo/use-mongo"
import { worthRetrying } from "@/components/database/redis/read-error"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { tabClasses } from "@/components/tabs"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { useEffect, useMemo, useRef, useState } from "react"

/** How often the counters are read: one `serverStatus` each time. */
const EVERY_MS = 3000

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
 *
 * A server that only speaks the protocol may answer with no counters at all.
 * It is then said to have none — a chart of zeros would read as a server
 * at rest — and the page keeps what such a server does report: what it is,
 * and what its databases hold.
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
  const silent = now !== undefined && reportsNothing(now)

  return (
    <SectionFrame section="performance">
      {dialog}
      {silent && (
        <BlockedState engine={engine} thing="performance counters">
          This server reports no activity counters. Its database details are below.
        </BlockedState>
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
            // One view is no choice: the strip is drawn where there is another to go to.
            className={cn(
              "flex gap-1 overflow-x-auto border-b border-hairline",
              views.length < 2 && "hidden",
            )}
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
            silent={silent}
          />
        )}
      </div>
    </SectionFrame>
  )
}
