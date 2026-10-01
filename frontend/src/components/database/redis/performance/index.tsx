"use client"

import { useMemo, useState } from "react"
import { bytes, percent } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { utilisationTone } from "@/components/meter"
import { TileTrend } from "@/components/metrics/sparkline"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Notice } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { SectionError, SectionFrame } from "@/components/database/kit"
import { redisStats } from "@/components/database/redis/api"
import { worthRetrying } from "@/components/database/redis/read-error"
import { ClientsView } from "@/components/database/redis/performance/clients"
import { CommandsView } from "@/components/database/redis/performance/commands"
import { MemoryView } from "@/components/database/redis/performance/memory"
import { OverviewView } from "@/components/database/redis/performance/overview"
import { PersistenceView } from "@/components/database/redis/performance/persistence"
import {
  addSample,
  lifetimeHitRate,
  perSecond,
  seriesOf,
  statRows,
  type StatSample,
} from "@/components/database/redis/performance/samples"
import { SlowlogView } from "@/components/database/redis/performance/slowlog"
import { useRedis } from "@/components/database/redis/use-redis"

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

  if (stats.error && samples.length === 0) {
    return (
      <SectionError
        section="performance"
        error={worthRetrying(stats.error)}
        onRetry={stats.refresh}
      />
    )
  }

  const now = newest?.counters
  const row = rows[rows.length - 1]
  const memory = server.data?.memory
  // Against its own limit where it has one, else against the machine's memory:
  // a server with no limit still has a ceiling, and it is the host's.
  const ceiling = now?.maxmemory || memory?.max || memory?.systemTotal || 0
  const used = now?.used_memory
  const fill = used !== undefined && ceiling > 0 ? (used / ceiling) * 100 : undefined
  const limited = Boolean(now?.maxmemory || memory?.max)
  const hitRate = now ? lifetimeHitRate(now) : null
  const clients = now?.connected_clients
  const spaces = server.data?.keyspace.length ?? 0

  return (
    <SectionFrame section="performance">
      {server.data?.notice && <Notice title="About this server">{server.data.notice}</Notice>}
      {stats.error && (
        <p role="status" className="text-hint text-warning">
          The last reading failed, so the figures are a few seconds old: {stats.error.message}
        </p>
      )}
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
            label="Commands a second"
            value={perSecond(row?.ops ?? now.instantaneous_ops_per_sec ?? 0)}
            trend={
              <TileTrend
                values={seriesOf(rows, "ops")}
                label="Commands a second"
                color="var(--chart-1)"
              />
            }
            hint={
              now.total_commands_processed === undefined
                ? undefined
                : `${now.total_commands_processed.toLocaleString()} since it started`
            }
          />
          <StatTile
            label="Memory"
            value={used === undefined ? "—" : bytes(used)}
            meter={fill}
            tone={limited && fill !== undefined ? utilisationTone(fill) : "default"}
            hint={
              used === undefined
                ? "The server does not report it"
                : limited
                  ? `of a ${bytes(ceiling)} limit${memory?.policy ? ` · ${memory.policy}` : ""}`
                  : ceiling > 0
                    ? `no limit · the machine has ${bytes(ceiling)}`
                    : "no limit set"
            }
          />
          <StatTile
            label="Hit rate"
            value={hitRate === null ? "—" : percent(hitRate)}
            trend={
              <TileTrend
                values={seriesOf(rows, "hitRate")}
                label="Hit rate"
                color="var(--chart-5)"
                max={100}
              />
            }
            hint={
              hitRate === null
                ? "No key has been looked up yet"
                : `${(now.keyspace_hits ?? 0).toLocaleString()} hits · ${(now.keyspace_misses ?? 0).toLocaleString()} misses`
            }
          />
          <StatTile
            label="Clients"
            value={clients === undefined ? "—" : clients.toLocaleString()}
            trend={
              <TileTrend
                values={seriesOf(rows, "clients")}
                label="Clients"
                color="var(--chart-2)"
              />
            }
            tone={now.blocked_clients ? "warning" : "default"}
            hint={
              now.blocked_clients
                ? `${now.blocked_clients.toLocaleString()} blocked on a command`
                : now.rejected_connections
                  ? `${now.rejected_connections.toLocaleString()} refused since it started`
                  : "none blocked"
            }
          />
          <StatTile
            label="Keys"
            value={now.keys === undefined ? "—" : now.keys.toLocaleString()}
            trend={
              <TileTrend values={seriesOf(rows, "keys")} label="Keys" color="var(--chart-4)" />
            }
            hint={[
              // The server counts its keys over every numbered database.
              spaces > 1 ? `in ${spaces} databases` : "",
              now.expires === undefined ? "" : `${now.expires.toLocaleString()} set to expire`,
            ]
              .filter(Boolean)
              .join(" · ")}
          />
        </StatGrid>
      )}

      <div className="min-w-0 space-y-6">
        <nav
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
        </nav>
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
