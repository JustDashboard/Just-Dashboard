"use client"

import Link from "next/link"
import { Trash } from "@/components/icons"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { useArrivals } from "@/hooks/use-arrivals"
import { cn } from "@/lib/utils"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { Metric, MetricStrip } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { EngineMark } from "@/components/database/kit"
import { redisSlowlog, redisSlowlogReset } from "@/components/database/redis/api"
import { bytesLabel } from "@/components/database/redis/bytes"
import { micros } from "@/components/database/redis/performance/samples"
import { ReadError } from "@/components/database/redis/read-error"
import type { Redis } from "@/components/database/redis/use-redis"

/**
 * The commands the server found slow, newest first.
 *
 * The server keeps the last so many commands that ran longer than its
 * threshold, with their arguments as it abbreviated them. Both figures are
 * the server's settings and are changed under Settings; emptying the log is
 * a removal, and is offered only to a role that may remove things.
 */
export function SlowlogView({ redis }: { redis: Redis }) {
  const { id, conn, engine, href, canDestroy } = redis
  const { confirm, dialog } = useConfirm()
  const log = usePoll((signal) => redisSlowlog(id, signal), 10_000, [id])
  const arrived = useArrivals(log.data?.entries.map((entry) => String(entry.id)) ?? [])

  if (log.error && !log.data) return <ReadError error={log.error} onRetry={log.refresh} />
  if (!log.data) return <LoadingPanel plain rows={6} />
  const data = log.data

  if (!data.supported) {
    return (
      <EmptyState
        mark={<EngineMark engine={engine} />}
        title="This server keeps no slow log"
        description={data.reason}
      />
    )
  }

  const off = data.thresholdUs !== undefined && data.thresholdUs < 0
  const slowest = data.entries.reduce((most, entry) => Math.max(most, entry.durationUs), 0)

  const reset = () =>
    confirm({
      title: "Empty the slow log",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: conn.name,
        facts: <FormFact label="Holds">{plural(data.length, "entry", "entries")}</FormFact>,
      },
      description:
        "Every entry the server has kept is discarded. It is the server's own log, so it is emptied for everybody who reads it.",
      confirmLabel: "Empty the slow log",
      action: async () => {
        await redisSlowlogReset(id)
      },
      onDone: log.refresh,
    })

  return (
    <div className="space-y-6">
      {dialog}
      <MetricStrip>
        <Metric
          label="Logs a command slower than"
          value={off ? "Nothing — it is off" : micros(data.thresholdUs ?? 0)}
        />
        <Metric label="Keeps the last" value={(data.maxLen ?? 0).toLocaleString()} />
        <Metric label="Holds now" value={data.length.toLocaleString()} />
        {slowest > 0 && <Metric label="Slowest here" value={micros(slowest)} />}
      </MetricStrip>

      {log.error && (
        <p role="status" className="text-hint text-warning">
          The log could not be read again just now: {log.error.message}
        </p>
      )}

      <Panel plain>
        <PanelHeader
          title="Slow commands"
          actions={
            <>
              <Button size="xs" variant="ghost" asChild>
                <Link href={href("settings")}>Change the threshold</Link>
              </Button>
              {canDestroy && engine.can("queryLogReset") && data.length > 0 && (
                <Button size="xs" variant="outline" onClick={reset}>
                  <Trash />
                  Empty the log
                </Button>
              )}
            </>
          }
        />
        <PanelBody flush className="group-data-[plain]/panel:-mx-4">
          {data.entries.length === 0 ? (
            <EmptyState
              className="mx-4 mt-4"
              title={off ? "The slow log is off" : "Nothing slow has been logged"}
              description={
                off
                  ? "slowlog-log-slower-than is negative, so the server records nothing. Set it under Settings."
                  : `No command has taken longer than ${micros(data.thresholdUs ?? 0)} since the log was last emptied.`
              }
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>When</TableHead>
                  <TableHead className="text-right">Took</TableHead>
                  <TableHead className="w-full">Command</TableHead>
                  <TableHead>Client</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.entries.map((entry) => (
                  <TableRow
                    key={entry.id}
                    className={cn(arrived.has(String(entry.id)) && "animate-rise")}
                  >
                    <TableCell className="text-muted-foreground" title={timestamp(entry.at)}>
                      {relativeTime(entry.at)}
                    </TableCell>
                    <TableCell className="numeric text-right font-medium">
                      {micros(entry.durationUs)}
                    </TableCell>
                    <TableCell className="max-w-0 font-mono">
                      <span className="block truncate" title={entry.args.map(bytesLabel).join(" ")}>
                        <span className="font-medium">{entry.command}</span>{" "}
                        <span className="text-muted-foreground">
                          {entry.args.map(bytesLabel).join(" ")}
                        </span>
                      </span>
                    </TableCell>
                    <TableCell className="font-mono text-muted-foreground">
                      {entry.clientName || entry.client || "—"}
                    </TableCell>
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
