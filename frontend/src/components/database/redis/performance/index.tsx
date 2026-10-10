"use client"

import { SectionError, SectionFrame } from "@/components/database/kit"
import { redisStats } from "@/components/database/redis/api"
import { ClientsView } from "@/components/database/redis/performance/clients"
import { CommandsView } from "@/components/database/redis/performance/commands"
import { MemoryView } from "@/components/database/redis/performance/memory"
import { OverviewView } from "@/components/database/redis/performance/overview"
import { PersistenceView } from "@/components/database/redis/performance/persistence"
import {
  addSample,
  statRows,
  type StatSample,
} from "@/components/database/redis/performance/samples"
import { SlowlogView } from "@/components/database/redis/performance/slowlog"
import { worthRetrying } from "@/components/database/redis/read-error"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useRedis } from "@/components/database/redis/use-redis"
import { Notice } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
import { usePoll } from "@/hooks/use-poll"
import { useEffect, useMemo, useRef, useState } from "react"

/** How often the counters are read: one INFO each time. */
const EVERY_MS = 3000

type View = "overview" | "memory" | "slowlog" | "clients" | "commands" | "persistence"

/**
 * What the server is doing: how busy it is, what its memory holds, who is
 * connected, and what has been slow.
 *
 * Five readings head the page — commands a second, memory against its
 * ceiling, the share of lookups that hit, clients, keys — each with the shape
 * it has had since the page was opened. They come from the server's counters,
 * sampled every few seconds; a rate is the difference between two samples,
 * so the first line of a trend appears with the second sample and nothing
 * here is recorded history.
 *
 * Under them the page reads six ways: the same samples as charts; a memory
 * analysis made on request; the slow log; the clients; the commands by the
 * time they have cost; and how the data is kept — on disk and on replicas.
 */
export function RedisPerformance() {
  const redis = useRedis()
  const { id, server, engine, param, select } = redis
  useFocusReturn()
  const stats = usePoll((signal) => redisStats(id, signal), EVERY_MS, [id])
  const [samples, setSamples] = useState<StatSample[]>([])
  const newest = samples[samples.length - 1]
  if (stats.data && (!newest || stats.data.sampledAtMs > newest.at)) {
    setSamples(addSample(samples, stats.data))
  }
  const rows = useMemo(() => statRows(samples), [samples])

  const views: { id: View; label: string }[] = [
    { id: "overview", label: "Overview" },
    ...(engine.can("memoryAnalysis") ? [{ id: "memory" as const, label: "Memory analysis" }] : []),
    ...(engine.can("queryLog") ? [{ id: "slowlog" as const, label: "Slow log" }] : []),
    ...(engine.can("sessions") ? [{ id: "clients" as const, label: "Clients" }] : []),
    ...(engine.can("commandStats") ? [{ id: "commands" as const, label: "Commands" }] : []),
    ...(engine.can("persistence") || engine.can("replication")
      ? [{ id: "persistence" as const, label: "Persistence & replication" }]
      : []),
  ]
  const asked = param("view")
  const view: View = views.some((entry) => entry.id === asked) ? (asked as View) : "overview"

  // On a phone the strip is wider than the page and scrolls sideways: the
  // view on screen is kept in sight in it, so the strip says where the
  // reader is and not only where they could go.
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

  const clients = newest?.counters.connected_clients

  return (
    <SectionFrame section="performance">
      {server.data?.notice && <Notice title="About this server">{server.data.notice}</Notice>}

      <div className="min-w-0 space-y-6">
        {/* A strip of pressed buttons, not a landmark: these are six readings
            of one page, and the rail is where the product navigates. The
            line under it is where a failed reading is said, so the page
            does not move when one fails. */}
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
                {entry.id === "clients" && clients !== undefined && (
                  <ChipCount>{clients.toLocaleString()}</ChipCount>
                )}
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
        {view === "overview" ? (
          <OverviewView redis={redis} rows={rows} everyMs={EVERY_MS} />
        ) : view === "memory" ? (
          <MemoryView redis={redis} />
        ) : view === "slowlog" ? (
          <SlowlogView redis={redis} />
        ) : view === "clients" ? (
          <ClientsView redis={redis} />
        ) : view === "commands" ? (
          <CommandsView redis={redis} />
        ) : (
          <PersistenceView redis={redis} />
        )}
      </div>
    </SectionFrame>
  )
}
