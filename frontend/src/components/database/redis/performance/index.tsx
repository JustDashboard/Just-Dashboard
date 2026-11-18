"use client"

import { joinRecorded } from "@/components/database/home/samples"
import { SectionError, SectionFrame } from "@/components/database/kit"
import { redisRecorded, redisStats } from "@/components/database/redis/api"
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
import { worthRetrying } from "@/components/database/kit/read-error"
import { useFocusTrail } from "@/hooks/use-focus-return"
import { useRedis } from "@/components/database/redis/use-redis"
import { Notice } from "@/components/state"
import { ViewTabs } from "@/components/tabs"
import { TabsContent } from "@/components/ui/tabs"
import { usePoll } from "@/hooks/use-poll"
import { useMemo, useState } from "react"

/** How often the counters are read: one INFO each time. */
const EVERY_MS = 3000

type View = "overview" | "memory" | "slowlog" | "clients" | "commands" | "persistence"

/**
 * What the server is doing: how busy it is, what its memory holds, who is
 * connected, and what has been slow.
 *
 * The Overview charts how busy it is, its memory against its ceiling, its
 * lookups, clients, network and the keys leaving it, each headed by its
 * newest reading. They come from the server's counters: the recorder's last
 * hour of them, every thirty seconds, then the page's own samples every few;
 * a rate is the difference between two, so the charts open drawn rather than
 * filling over the first minutes.
 *
 * The page reads six ways: those charts; a memory
 * analysis made on request; the slow log; the clients; the commands by the
 * time they have cost; and how the data is kept — on disk and on replicas.
 */
export function RedisPerformance() {
  const redis = useRedis()
  const { id, server, engine, param, select } = redis
  useFocusTrail()
  const stats = usePoll((signal) => redisStats(id, signal), EVERY_MS, [id])
  const [samples, setSamples] = useState<StatSample[]>([])
  const newest = samples[samples.length - 1]
  if (stats.data && (!newest || stats.data.sampledAtMs > newest.at)) {
    setSamples(addSample(samples, stats.data))
  }
  // The recorder's hour is read again each minute, so the stretch between it
  // and the oldest sample the page still holds stays filled.
  const recorded = usePoll((signal) => redisRecorded(id, signal), 60_000, [id])
  const rows = useMemo(
    () => statRows(joinRecorded(recorded.data ?? [], samples, (sample) => sample.at)),
    [recorded.data, samples],
  )

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

      <div className="min-w-0">
        {/* The views of one page, as tabs: the strip scrolls the chosen one
            into sight on a phone. A failed reading is said in the gap under
            the strip, so the page does not move when one fails. */}
        <ViewTabs
          label="Performance views"
          views={views.map((entry) => ({
            key: entry.id,
            label: entry.label,
            count: entry.id === "clients" ? clients : undefined,
          }))}
          value={view}
          onValueChange={(next) => select({ view: next === "overview" ? null : next })}
          hideWhenSingle
          rootClassName="gap-6"
        >
          <TabsContent value={view} className="relative min-w-0">
            {stats.error && (
              <p
                role="status"
                className="absolute inset-x-0 bottom-full mb-1.5 truncate text-hint text-warning"
              >
                The last reading failed, so the figures are a few seconds old: {stats.error.message}
              </p>
            )}
            {view === "overview" ? (
              <OverviewView
                redis={redis}
                rows={rows}
                everyMs={EVERY_MS}
                recorded={(recorded.data?.length ?? 0) > 0}
              />
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
          </TabsContent>
        </ViewTabs>
      </div>
    </SectionFrame>
  )
}
