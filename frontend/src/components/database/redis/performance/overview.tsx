"use client"

import Link from "next/link"
import { bytes, duration, rate, relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { Series } from "@/components/metrics/metric-chart"
import { RowLink } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, LoadingRows } from "@/components/state"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { redisLatency } from "@/components/database/redis/api"
import { perSecond, type StatRow } from "@/components/database/redis/performance/samples"
import { ReadError } from "@/components/database/redis/read-error"
import type { Redis } from "@/components/database/redis/use-redis"

// Module constants: `ChartPanel` is memoised on its props, and a series or a
// formatter made fresh each render would redraw every chart on every sample.
const OPS: Series[] = [{ key: "ops", label: "Commands", color: "var(--chart-1)", kind: "area" }]
const MEMORY: Series[] = [
  { key: "memory", label: "Used by data", color: "var(--chart-2)", kind: "area" },
  { key: "rss", label: "Held from the system", color: "var(--chart-4)", kind: "line" },
]
const LOOKUPS: Series[] = [
  { key: "hits", label: "Hits", color: "var(--chart-5)", kind: "area" },
  { key: "misses", label: "Misses", color: "var(--chart-3)", kind: "line" },
]
const CLIENTS: Series[] = [
  { key: "clients", label: "Connected", color: "var(--chart-2)", kind: "area" },
  { key: "blocked", label: "Blocked", color: "var(--chart-3)", kind: "line" },
]
const NETWORK: Series[] = [
  { key: "netIn", label: "Received", color: "var(--chart-2)", kind: "area" },
  { key: "netOut", label: "Sent", color: "var(--chart-5)", kind: "area" },
]
const EXPIRY: Series[] = [
  { key: "expired", label: "Expired", color: "var(--chart-4)", kind: "area" },
  { key: "evicted", label: "Evicted", color: "var(--chart-3)", kind: "line" },
]

const perSecondLabel = (value: number) => `${perSecond(value)}/s`
const bytesLabel = (value: number) => bytes(value)
const bytesAxis = (value: number) => bytes(value, 0)
const rateLabel = (value: number) => rate(value)
const countLabel = (value: number) =>
  Number.isInteger(value) ? value.toLocaleString() : value.toFixed(2)

/**
 * The page's own samples as charts, and the two readings that sit beside
 * them: how the keys are spread over the numbered databases, and the spikes
 * the server's latency monitor has caught.
 *
 * The charts are live samples and say so. A rate needs two of them, so a
 * chart is empty for the first few seconds, and a series the server does not
 * report is left out rather than drawn at zero.
 */
export function OverviewView({
  redis,
  rows,
  everyMs,
}: {
  redis: Redis
  rows: StatRow[]
  everyMs: number
}) {
  const { id, server, href, engine } = redis
  const latency = usePoll((signal) => redisLatency(id, signal), 15_000, [id], {
    enabled: engine.can("latency"),
  })
  const waiting = `A rate is the difference between two readings: the first appears ${Math.round((everyMs * 2) / 1000)} seconds after the page opens.`
  const keyspace = server.data?.keyspace

  return (
    <div className="space-y-8">
      <div className="grid gap-x-8 gap-y-6 lg:grid-cols-2 [&>*]:min-w-0">
        <ChartPanel
          plain
          title="Throughput"
          rows={rows}
          series={OPS}
          format={perSecondLabel}
          note={waiting}
        />
        <ChartPanel
          plain
          title="Memory"
          rows={rows}
          series={MEMORY}
          format={bytesLabel}
          axisFormat={bytesAxis}
          note="The server does not report its memory."
        />
        <ChartPanel
          plain
          title="Lookups"
          rows={rows}
          series={LOOKUPS}
          format={perSecondLabel}
          note={waiting}
        />
        <ChartPanel plain title="Clients" rows={rows} series={CLIENTS} format={countLabel} />
        <ChartPanel
          plain
          title="Network"
          rows={rows}
          series={NETWORK}
          format={rateLabel}
          note={waiting}
        />
        <ChartPanel
          plain
          title="Keys leaving"
          rows={rows}
          series={EXPIRY}
          format={perSecondLabel}
          note={waiting}
        />
      </div>
      <p className="text-hint text-muted-foreground">
        Sampled every {Math.round(everyMs / 1000)} seconds since this page was opened. Nothing here
        is recorded: the charts start again when the page does.
      </p>

      <div className="grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0">
        <Panel plain>
          <PanelHeader title="Databases" />
          <PanelBody flush className="group-data-[plain]/panel:-mx-4">
            {server.error && !server.data ? (
              <ReadError error={server.error} onRetry={server.refresh} className="my-3" />
            ) : !keyspace ? (
              <LoadingRows rows={3} className="py-3" />
            ) : keyspace.length === 0 ? (
              <EmptyNote>No database holds a key.</EmptyNote>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead>Database</TableHead>
                    <TableHead className="text-right">Keys</TableHead>
                    <TableHead className="text-right">Set to expire</TableHead>
                    <TableHead className="text-right">Average time left</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {keyspace.map((space) => (
                    <TableRow key={space.db}>
                      <TableCell>
                        <RowLink mono onClick={() => redis.goto("data", { db: String(space.db) })}>
                          db{space.db}
                          {space.db === server.data?.db ? " · connects here" : ""}
                        </RowLink>
                      </TableCell>
                      <TableCell className="numeric text-right">
                        {space.keys.toLocaleString()}
                      </TableCell>
                      <TableCell className="numeric text-right">
                        {space.expires.toLocaleString()}
                      </TableCell>
                      <TableCell className="numeric text-right text-muted-foreground">
                        {space.expires > 0 ? duration(space.avgTtlMs / 1000) : "—"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </PanelBody>
        </Panel>

        {engine.can("latency") && (
          <Panel plain>
            <PanelHeader title="Latency spikes" />
            <PanelBody flush className="group-data-[plain]/panel:-mx-4">
              {latency.error && !latency.data ? (
                <ReadError error={latency.error} onRetry={latency.refresh} className="my-3" />
              ) : !latency.data ? (
                <LoadingRows rows={3} className="py-3" />
              ) : latency.data.events.length === 0 ? (
                <EmptyNote className="text-pretty">
                  {latency.data.reason ??
                    `Nothing has taken longer than ${latency.data.thresholdMs ?? 0} ms.`}{" "}
                  {!latency.data.enabled && latency.data.supported && (
                    <>
                      Set <span className="font-mono text-xs">latency-monitor-threshold</span> under{" "}
                      <Link href={href("settings")} className="underline underline-offset-2">
                        Settings
                      </Link>{" "}
                      to record them.
                    </>
                  )}
                </EmptyNote>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead>What stalled</TableHead>
                      <TableHead className="text-right">Latest</TableHead>
                      <TableHead className="text-right">Worst</TableHead>
                      <TableHead className="text-right">Last seen</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {latency.data.events.map((event) => (
                      <TableRow key={event.event}>
                        <TableCell className="font-mono">{event.event}</TableCell>
                        <TableCell className="numeric text-right">{event.latestMs} ms</TableCell>
                        <TableCell className="numeric text-right">{event.maxMs} ms</TableCell>
                        <TableCell className="text-right text-muted-foreground">
                          {relativeTime(event.at)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </PanelBody>
          </Panel>
        )}
      </div>
    </div>
  )
}
