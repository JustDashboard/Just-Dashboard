"use client"

import { useMemo } from "react"
import { bytes, duration, rate } from "@/lib/format"
import type { PollState } from "@/hooks/use-poll"
import { ChartPanel } from "@/components/metrics/chart-panel"
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
import type { StatRow } from "@/components/database/mongo/performance/samples"
import { DatabaseMark } from "@/components/database/mongo/rail"
import type { MongoDatabase, MongoServer } from "@/components/database/mongo/types"
import type { Mongo } from "@/components/database/mongo/use-mongo"
import { countScale, micros, perSecond } from "@/components/database/redis/performance/samples"
import { ReadError } from "@/components/database/redis/read-error"

// Module constants: `ChartPanel` is memoised on its props, and a series or a
// formatter made fresh each render would redraw every chart on every sample.
const OPERATIONS: Series[] = [
  { key: "reads", label: "Queries", color: "var(--chart-1)", kind: "area" },
  { key: "insert", label: "Inserts", color: "var(--chart-5)", kind: "area" },
  { key: "update", label: "Updates", color: "var(--chart-4)", kind: "area" },
  { key: "delete", label: "Deletes", color: "var(--chart-3)", kind: "area" },
  { key: "command", label: "Commands", color: "var(--chart-2)", kind: "area" },
]
const CONNECTIONS: Series[] = [
  { key: "connections", label: "Open", color: "var(--chart-2)", kind: "area" },
  { key: "active", label: "Doing work", color: "var(--chart-1)", kind: "line" },
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
 * The page's own samples as charts, and the two readings that sit beside
 * them: what each database on the server holds, and what the server is.
 *
 * The charts are live samples and say so. A rate needs two of them, so a
 * chart is empty for the first few seconds, and a series the server does not
 * report is left out rather than drawn at zero.
 */
export function OverviewView({
  mongo,
  rows,
  server,
  databases,
  everyMs,
}: {
  mongo: Mongo
  rows: StatRow[]
  server: MongoServer | undefined
  databases: PollState<MongoDatabase[]>
  everyMs: number
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

  return (
    <div className="space-y-8">
      <div className="grid gap-x-8 gap-y-6 lg:grid-cols-2 [&>*]:min-w-0">
        <ChartPanel
          plain
          stacked
          title="Operations"
          rows={charted}
          series={OPERATIONS}
          format={perSecondLabel}
          note={waiting}
        />
        <ChartPanel
          plain
          title="Connections"
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
            rows={charted}
            series={CACHE}
            format={bytesLabel}
            axisFormat={bytesAxis}
          />
        )}
        <ChartPanel
          plain
          title="Documents"
          rows={charted}
          series={DOCUMENTS}
          format={perSecondLabel}
          note={waiting}
        />
        <ChartPanel
          plain
          title="Time an operation takes"
          rows={charted}
          series={LATENCY}
          format={microsLabel}
          note="Shown once an operation of each kind has run between two readings."
        />
        <ChartPanel
          plain
          title="Network"
          rows={charted}
          series={NETWORK}
          format={rateLabel}
          note={waiting}
        />
      </div>
      <p className="text-hint text-muted-foreground">
        Sampled every {Math.round(everyMs / 1000)} seconds since this page was opened. Nothing here
        is recorded: the charts start again when the page does.
      </p>

      <div className="grid items-start gap-8 lg:grid-cols-3 [&>*]:min-w-0">
        <Panel plain className="lg:col-span-2">
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
                <Detail label="Storage engine">{server.storageEngine || "—"}</Detail>
                <Detail label="Role">
                  {server.topology === "replicaset"
                    ? `${server.role} of ${server.setName ?? "a replica set"}`
                    : server.topology === "sharded"
                      ? "a router of a sharded cluster"
                      : "a standalone server"}
                </Detail>
                <Detail label="Up for">{duration(server.uptime)}</Detail>
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
              </DetailList>
            )}
          </PanelBody>
        </Panel>
      </div>
    </div>
  )
}
