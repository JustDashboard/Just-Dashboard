"use client"

import { useMemo } from "react"
import { bytes, duration, rate } from "@/lib/format"
import type { PollState } from "@/hooks/use-poll"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { ChartReadings, SeriesReading } from "@/components/metrics/chart-reading"
import type { Series } from "@/components/metrics/metric-chart"
import { Detail, DetailList, RowLink } from "@/components/page"
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
import { BusiestCollections } from "@/components/database/mongo/performance/busiest"
import { latest, type StatRow } from "@/components/database/mongo/performance/samples"
import { DatabaseMark } from "@/components/database/mongo/rail"
import type { MongoDatabase, MongoServer } from "@/components/database/mongo/types"
import type { Mongo } from "@/components/database/mongo/use-mongo"
import { countScale, micros, perSecond } from "@/components/database/redis/performance/samples"
import { ReadError } from "@/components/database/redis/read-error"

// Module constants: `ChartPanel` is memoised on its props, and a series or a
// formatter made fresh each render would redraw every chart on every sample.
// Each line in its measurement's hue (§10), the database home's: connections
// blue with the working green, as a SQL server's sessions are.
const OPERATIONS: Series[] = [
  { key: "reads", label: "Queries", color: "var(--chart-1)", kind: "area" },
  { key: "insert", label: "Inserts", color: "var(--chart-5)", kind: "area" },
  { key: "update", label: "Updates", color: "var(--chart-4)", kind: "area" },
  { key: "delete", label: "Deletes", color: "var(--chart-3)", kind: "area" },
  { key: "command", label: "Commands", color: "var(--chart-2)", kind: "area" },
]
/** The stack's total, as its head reads it: every kind at once. */
const ALL_OPERATIONS = { label: "Operations", color: "var(--chart-2)" }
const CONNECTIONS: Series[] = [
  { key: "connections", label: "Open", color: "var(--chart-2)", kind: "area" },
  { key: "active", label: "Doing work", color: "var(--chart-5)", kind: "line" },
]
const CACHE: Series[] = [
  { key: "cacheBytes", label: "In the cache", color: "var(--chart-2)", kind: "area" },
  { key: "dirtyBytes", label: "Not yet written", color: "var(--chart-3)", kind: "line" },
]
const NETWORK: Series[] = [
  { key: "netIn", label: "Received", color: "var(--chart-2)", kind: "area" },
  { key: "netOut", label: "Sent", color: "var(--chart-5)", kind: "area" },
]
const DOCUMENTS: Series[] = [
  { key: "scanned", label: "Examined", color: "var(--chart-3)", kind: "area" },
  { key: "returned", label: "Returned", color: "var(--chart-5)", kind: "line" },
]
const LATENCY: Series[] = [
  { key: "readLatency", label: "Reads", color: "var(--chart-1)", kind: "line" },
  { key: "writeLatency", label: "Writes", color: "var(--chart-4)", kind: "line" },
  { key: "commandLatency", label: "Commands", color: "var(--chart-2)", kind: "line" },
]

const perSecondLabel = (value: number) => `${perSecond(value)}/s`
const bytesLabel = (value: number) => bytes(value)
const bytesAxis = (value: number) => bytes(value, 0)
const rateLabel = (value: number) => rate(value)
const countLabel = (value: number) =>
  Number.isInteger(value) ? value.toLocaleString("en-US") : value.toFixed(2)
const wholeLabel = (value: number) => Math.round(value).toLocaleString("en-US")
const microsLabel = (value: number) => micros(value)

const grouped = (n: number) => n.toLocaleString("en-US")

/**
 * The server's counters as charts, each headed by its newest reading, and the
 * readings that sit under them: which collections the work is on, what the
 * server is, and what each database on it holds.
 *
 * The charts open on the recorder's last hour and carry on with the page's
 * own samples. A series the server does not report is left out rather than
 * drawn at zero.
 */
export function OverviewView({
  mongo,
  rows,
  server,
  databases,
  everyMs,
  silent,
  recorded,
}: {
  mongo: Mongo
  rows: StatRow[]
  server: MongoServer | undefined
  databases: PollState<MongoDatabase[]>
  everyMs: number
  /** The server reports no counters: there is nothing to chart, and the page says so above. */
  silent: boolean
  /** The rows open on the recorder's hour, before the page's own samples. */
  recorded: boolean
}) {
  const waiting = `A rate is the difference between two readings: the first appears ${Math.round((everyMs * 2) / 1000)} seconds after the page opens.`
  // Queries and the cursors that continue them are one kind of work to a reader.
  const charted = useMemo(
    () =>
      rows.map((row) => ({
        ...row,
        reads: row.query === null ? null : row.query + (row.getmore ?? 0),
      })),
    [rows],
  )
  // Connections come whole: the axis is whole numbers up to the most seen. Held
  // by that figure, so the chart's props stay the same from sample to sample.
  const most = rows.reduce((top, row) => Math.max(top, row.connections, row.active), 0)
  const connectionScale = useMemo(() => countScale(most), [most])
  const hasCache = rows.some((row) => row.cacheBytes !== null)
  const limit = server ? server.connections.current + server.connections.available : 0
  const open = latest(rows, "connections")
  const cacheCeiling = server?.cache?.maxBytes ?? 0
  const cached = latest(rows, "cacheBytes")

  const serverPanel = (
    <Panel plain>
      <PanelHeader title="The server" />
      <PanelBody>
        {!server ? (
          <LoadingRows rows={5} />
        ) : (
          <DetailList>
            <Detail label="Host">
              <span className="font-mono">{server.host}</span>
            </Detail>
            <Detail label="Version">
              {server.process} {server.version}
            </Detail>
            <Detail label="Role">
              {server.topology === "replicaset"
                ? `${server.role} of ${server.setName ?? "a replica set"}`
                : server.topology === "sharded"
                  ? "a router of a sharded cluster"
                  : "a standalone server"}
            </Detail>
            <Detail label="Up for">{duration(server.uptime)}</Detail>
            {/* A server that reports no counters reports none of these either. */}
            {!silent && (
              <>
                <Detail label="Storage engine">{server.storageEngine || "—"}</Detail>
                <Detail label="Memory held">{bytes(server.memory.resident * 1024 * 1024)}</Detail>
                <Detail label="Cursors open">
                  {grouped(server.cursors.open)}
                  {server.cursors.timedOut > 0
                    ? ` · ${grouped(server.cursors.timedOut)} timed out`
                    : ""}
                </Detail>
                <Detail label="Waiting for a lock">
                  {grouped(server.queue.queuedReaders)} readers ·{" "}
                  {grouped(server.queue.queuedWriters)} writers
                </Detail>
              </>
            )}
          </DetailList>
        )}
      </PanelBody>
    </Panel>
  )

  return (
    <div className="space-y-8">
      {!silent && (
        <>
          <div className="grid gap-x-8 gap-y-6 lg:grid-cols-2 [&>*]:min-w-0">
            <ChartPanel
              plain
              stacked
              title="Operations"
              actions={
                <ChartReadings>
                  <SeriesReading
                    series={ALL_OPERATIONS}
                    value={latest(rows, "ops")}
                    format={perSecondLabel}
                    note="operations"
                  />
                </ChartReadings>
              }
              rows={charted}
              series={OPERATIONS}
              format={perSecondLabel}
              note={waiting}
            />
            <ChartPanel
              plain
              title="Connections"
              actions={
                <ChartReadings>
                  <SeriesReading
                    series={CONNECTIONS[0]}
                    value={open}
                    format={wholeLabel}
                    note={limit > 0 ? `of ${grouped(limit)}` : "open"}
                    meter={limit > 0 && open !== null ? (open / limit) * 100 : undefined}
                  />
                  <SeriesReading
                    series={CONNECTIONS[1]}
                    value={latest(rows, "active")}
                    format={wholeLabel}
                    note="working"
                  />
                </ChartReadings>
              }
              rows={charted}
              series={CONNECTIONS}
              format={countLabel}
              axisFormat={wholeLabel}
              domain={connectionScale.domain}
              yTicks={connectionScale.ticks}
            />
            {hasCache && (
              <ChartPanel
                plain
                title="Cache"
                actions={
                  <ChartReadings>
                    <SeriesReading
                      series={CACHE[0]}
                      value={cached}
                      format={bytesLabel}
                      note={cacheCeiling > 0 ? `of ${bytes(cacheCeiling)}` : "cached"}
                      meter={
                        cacheCeiling > 0 && cached !== null
                          ? (cached / cacheCeiling) * 100
                          : undefined
                      }
                    />
                  </ChartReadings>
                }
                rows={charted}
                series={CACHE}
                format={bytesLabel}
                axisFormat={bytesAxis}
              />
            )}
            <ChartPanel
              plain
              title="Documents"
              actions={
                <ChartReadings>
                  <SeriesReading
                    series={DOCUMENTS[0]}
                    value={latest(rows, "scanned")}
                    format={perSecondLabel}
                    note="examined"
                  />
                  <SeriesReading
                    series={DOCUMENTS[1]}
                    value={latest(rows, "returned")}
                    format={perSecondLabel}
                    note="returned"
                  />
                </ChartReadings>
              }
              rows={charted}
              series={DOCUMENTS}
              format={perSecondLabel}
              note={waiting}
            />
            <ChartPanel
              plain
              title="Time an operation takes"
              actions={
                <ChartReadings>
                  <SeriesReading
                    series={LATENCY[0]}
                    value={latest(rows, "readLatency")}
                    format={microsLabel}
                    note="a read"
                  />
                  <SeriesReading
                    series={LATENCY[1]}
                    value={latest(rows, "writeLatency")}
                    format={microsLabel}
                    note="a write"
                  />
                </ChartReadings>
              }
              rows={charted}
              series={LATENCY}
              format={microsLabel}
              note="Shown once an operation of each kind has run between two readings."
            />
            <ChartPanel
              plain
              title="Network"
              actions={
                <ChartReadings>
                  <SeriesReading
                    series={NETWORK[0]}
                    value={latest(rows, "netIn")}
                    format={rateLabel}
                    note="in"
                  />
                  <SeriesReading
                    series={NETWORK[1]}
                    value={latest(rows, "netOut")}
                    format={rateLabel}
                    note="out"
                  />
                </ChartReadings>
              }
              rows={charted}
              series={NETWORK}
              format={rateLabel}
              note={waiting}
            />
          </div>
          <p className="text-hint text-muted-foreground">
            {recorded
              ? `The last hour as the recorder kept it, every 30 seconds, then sampled every ${Math.round(everyMs / 1000)} seconds since this page was opened. How long an operation takes is read only here, so its chart starts with the page.`
              : `Sampled every ${Math.round(everyMs / 1000)} seconds since this page was opened. The recorder has kept nothing for this server yet.`}
          </p>
        </>
      )}

      <div className="grid items-start gap-8 lg:grid-cols-3 [&>*]:min-w-0">
        {/* Not drawn on a server that keeps no such counts: the panel beside it then leads the row. */}
        <div className="empty:hidden lg:col-span-2">
          <BusiestCollections mongo={mongo} />
        </div>
        {serverPanel}
      </div>

      <Panel plain>
        <PanelHeader title="Databases on this server" />
        <PanelBody flush className="group-data-[plain]/panel:-mx-4">
          {databases.error && !databases.data ? (
            <ReadError error={databases.error} onRetry={databases.refresh} className="my-3" />
          ) : !databases.data ? (
            <LoadingRows rows={4} className="py-3" />
          ) : databases.data.length === 0 ? (
            <EmptyNote>The server holds no database yet.</EmptyNote>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Database</TableHead>
                  <TableHead className="text-right">Collections</TableHead>
                  <TableHead className="text-right">Documents</TableHead>
                  <TableHead className="text-right">Data</TableHead>
                  <TableHead className="text-right max-sm:hidden">On disk</TableHead>
                  <TableHead className="text-right max-sm:hidden">Indexes</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {databases.data.map((entry) => (
                  <TableRow key={entry.name}>
                    <TableCell>
                      <span className="flex min-w-0 items-center gap-2">
                        <DatabaseMark name={entry.name} />
                        <RowLink
                          mono
                          title={`Open ${entry.name} in Documents`}
                          onClick={() => mongo.goto("data", { db: entry.name, collection: null })}
                        >
                          {entry.name}
                        </RowLink>
                        {entry.name === mongo.conn.database && (
                          <span className="shrink-0 text-hint text-muted-foreground">
                            connects here
                          </span>
                        )}
                      </span>
                    </TableCell>
                    {entry.statsKnown ? (
                      <>
                        <TableCell className="numeric text-right">
                          {grouped(entry.collections)}
                        </TableCell>
                        <TableCell className="numeric text-right">
                          {grouped(entry.objects)}
                        </TableCell>
                        <TableCell className="numeric text-right">
                          {bytes(entry.dataSize)}
                        </TableCell>
                        <TableCell className="numeric text-right text-muted-foreground max-sm:hidden">
                          {bytes(entry.storageSize)}
                        </TableCell>
                        <TableCell className="numeric text-right text-muted-foreground max-sm:hidden">
                          {grouped(entry.indexes)} · {bytes(entry.indexSize)}
                        </TableCell>
                      </>
                    ) : (
                      <TableCell colSpan={5} className="text-right text-muted-foreground">
                        this account may not measure it
                      </TableCell>
                    )}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}
