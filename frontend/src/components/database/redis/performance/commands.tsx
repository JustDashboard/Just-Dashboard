"use client"

import { useState } from "react"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { Metric, MetricStrip } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, LoadingPanel } from "@/components/state"
import { FilterChip } from "@/components/tabs"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { redisCommandStats } from "@/components/database/redis/api"
import { micros, perSecond } from "@/components/database/redis/performance/samples"
import { ReadError } from "@/components/database/redis/read-error"
import type { RedisCommandStat, RedisCommandStats } from "@/components/database/redis/types"
import type { Redis } from "@/components/database/redis/use-redis"

type Order = "usec" | "calls" | "usecPerCall"

const ORDERS: { id: Order; label: string }[] = [
  { id: "usec", label: "Total time" },
  { id: "calls", label: "Calls" },
  { id: "usecPerCall", label: "Time per call" },
]

/**
 * What the server's time went on, command by command.
 *
 * The figures count from when the server started. Total time is the order
 * that says where the load is — a command a microsecond long, run a million
 * times, weighs more than a slow one run once — and the bar under each name
 * is its share of all of it. Two readings of the same counters give how
 * often each is being called now.
 */
export function CommandsView({ redis }: { redis: Redis }) {
  const { id } = redis
  const stats = usePoll((signal) => redisCommandStats(id, signal), 10_000, [id])
  const [order, setOrder] = useState<Order>("usec")
  // The reading before the newest, to say how often a command runs now.
  const [pair, setPair] = useState<{ before?: RedisCommandStats; now?: RedisCommandStats }>({})
  if (stats.data && pair.now?.sampledAtMs !== stats.data.sampledAtMs) {
    setPair({ before: pair.now, now: stats.data })
  }

  if (stats.error && !stats.data) return <ReadError error={stats.error} onRetry={stats.refresh} />
  if (!stats.data) return <LoadingPanel plain rows={8} />
  const data = stats.data

  const earlier = new Map(pair.before?.commands.map((command) => [command.command, command.calls]))
  const seconds = pair.before ? (data.sampledAtMs - pair.before.sampledAtMs) / 1000 : 0
  const nowRate = (command: RedisCommandStat): number | null => {
    const before = earlier.get(command.command)
    if (before === undefined || seconds <= 0 || command.calls < before) return null
    return (command.calls - before) / seconds
  }
  const rows = [...data.commands].sort((a, b) => b[order] - a[order])
  const percentiles = rows.some((command) => command.p99Us !== undefined)
  const most = Math.max(...rows.map((command) => command[order]), 1)

  return (
    <div className="space-y-6">
      <MetricStrip>
        <Metric
          label="Commands run"
          value={data.totalCalls.toLocaleString()}
          hint="since it started"
        />
        <Metric label="Time spent running them" value={micros(data.totalUsec)} />
        <Metric
          label="On average"
          value={data.totalCalls > 0 ? micros(data.totalUsec / data.totalCalls) : "—"}
          hint="a command"
        />
        <Metric label="Different commands" value={data.commands.length.toLocaleString()} />
      </MetricStrip>
      {stats.error && (
        <p role="status" className="text-hint text-warning">
          The statistics could not be read again just now: {stats.error.message}
        </p>
      )}
      <Panel plain>
        <PanelHeader
          title="Commands"
          actions={
            <div role="group" aria-label="Order" className="flex gap-0.5">
              {ORDERS.map((entry) => (
                <FilterChip
                  key={entry.id}
                  selected={order === entry.id}
                  onClick={() => setOrder(entry.id)}
                >
                  {entry.label}
                </FilterChip>
              ))}
            </div>
          }
        />
        <PanelBody flush className="group-data-[plain]/panel:-mx-4">
          {rows.length === 0 ? (
            <EmptyNote>No command has been run yet.</EmptyNote>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-2/5">Command</TableHead>
                  <TableHead className="text-right">Total time</TableHead>
                  <TableHead className="text-right">Calls</TableHead>
                  <TableHead className="text-right">Now</TableHead>
                  <TableHead className="text-right">Per call</TableHead>
                  {percentiles && <TableHead className="text-right">Median</TableHead>}
                  {percentiles && <TableHead className="text-right">99th</TableHead>}
                  <TableHead className="text-right">Failed</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((command) => {
                  const now = nowRate(command)
                  const failed = (command.failed ?? 0) + (command.rejected ?? 0)
                  return (
                    <TableRow key={command.command}>
                      <TableCell className="py-2">
                        <span className="block font-mono font-medium">{command.command}</span>
                        <span
                          aria-hidden
                          className="mt-1.5 block h-1 w-full overflow-hidden rounded-full bg-meter-track"
                        >
                          <span
                            className="block h-full rounded-full bg-primary transition-[width]"
                            style={{ width: `${Math.max((command[order] / most) * 100, 1.5)}%` }}
                          />
                        </span>
                      </TableCell>
                      <TableCell className="numeric text-right">
                        {micros(command.usec)}
                        <span className="ml-1.5 text-muted-foreground">
                          {data.totalUsec > 0
                            ? `${((command.usec / data.totalUsec) * 100).toFixed(1)}%`
                            : ""}
                        </span>
                      </TableCell>
                      <TableCell className="numeric text-right">
                        {command.calls.toLocaleString()}
                      </TableCell>
                      <TableCell className="numeric text-right text-muted-foreground">
                        {now === null ? "—" : `${perSecond(now)}/s`}
                      </TableCell>
                      <TableCell className="numeric text-right">
                        {micros(command.usecPerCall)}
                      </TableCell>
                      {percentiles && (
                        <TableCell className="numeric text-right text-muted-foreground">
                          {command.p50Us === undefined ? "—" : micros(command.p50Us)}
                        </TableCell>
                      )}
                      {percentiles && (
                        <TableCell className="numeric text-right text-muted-foreground">
                          {command.p99Us === undefined ? "—" : micros(command.p99Us)}
                        </TableCell>
                      )}
                      <TableCell
                        className={cn(
                          "numeric text-right",
                          failed > 0 ? "text-warning" : "text-muted-foreground",
                        )}
                      >
                        {failed.toLocaleString()}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}
