"use client"

import { useEffect, useRef, useState } from "react"
import { ApiError, api, get } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFact, Statement } from "@/components/form"
import { Metric } from "@/components/page"
import { Well } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { grouped, readableDuration } from "@/components/database/data/view"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { qualified } from "@/components/database/schema/changes"
import { Facts } from "@/components/database/schema/facts"
import type {
  DbIndexStats,
  DbMaintenanceAction,
  DbMaintenanceResult,
  DbTableDetail,
  DbTableStat,
  DbTableStats,
} from "@/components/database/schema/types"

/** How many tables are asked for: the server's ceiling, so the one wanted is among them. */
const MOST = 1000

/** A count the engine keeps; -1 is "does not say". */
const kept = (value: number | undefined): value is number => value !== undefined && value >= 0

/**
 * What the engine has measured of one table: where its bytes are, how it is
 * read and written, when it was last tended — and what each index costs and
 * earns.
 *
 * Every figure is the engine's own and is drawn only where it keeps one: a
 * count it does not keep is left out, never shown as zero. The maintenance
 * the engine offers for a single table sits under the figures that say
 * whether it is needed; an action is drawn for the role it asks for, and one
 * that locks the table is confirmed by name first.
 */
export function TableStatistics({
  detail,
  mayRun,
}: {
  detail: DbTableDetail
  /** The connection takes changes here: not protected, and a schema of the reader's own. */
  mayRun: boolean
}) {
  const { id, engine } = useDatabase()
  const stats = usePoll(
    (signal) =>
      get<DbTableStats>(
        `/databases/${id}/tablestats`,
        { schema: detail.schema || undefined, limit: MOST },
        signal,
      ),
    0,
    [id, detail.schema],
    { enabled: engine.can("tableStats") },
  )
  const indexes = usePoll(
    (signal) =>
      get<DbIndexStats>(
        `/databases/${id}/indexstats`,
        { schema: detail.schema || undefined, table: detail.name, limit: MOST },
        signal,
      ),
    0,
    [id, detail.schema, detail.name],
    { enabled: engine.can("indexStats") },
  )
  const stat = stats.data?.tables?.find(
    (entry) => entry.table === detail.name && (entry.schema === detail.schema || !entry.schema),
  )
  const refresh = () => {
    stats.refresh()
    indexes.refresh()
  }

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="space-y-6 p-4">
        {engine.can("tableStats") &&
          (stats.error && !stats.data ? (
            <ReadFailed error={stats.error} onRetry={stats.refresh} />
          ) : !stats.data ? (
            <FiguresSkeleton />
          ) : !stats.data.supported ? (
            <EmptyNote className="py-2 text-left">
              {stats.data.reason ?? `${engine.label} keeps no statistics for a table.`}
            </EmptyNote>
          ) : stat ? (
            <Figures stat={stat} notes={stats.data.notes} />
          ) : (
            <EmptyNote className="py-2 text-left">
              {stats.data.truncated
                ? `The server reported its ${grouped(MOST)} largest tables and this one is not among them.`
                : "The engine reported no statistics for this table."}
            </EmptyNote>
          ))}

        {engine.can("maintenance") && mayRun && <Maintenance detail={detail} onRan={refresh} />}

        {engine.can("indexStats") && (
          <section aria-label="Index use" className="min-w-0">
            <h3 className="border-b border-hairline pb-2 text-title font-medium">Index use</h3>
            {indexes.error && !indexes.data ? (
              <ReadFailed error={indexes.error} onRetry={indexes.refresh} className="pt-3" />
            ) : !indexes.data ? (
              <FiguresSkeleton />
            ) : !indexes.data.supported ? (
              <EmptyNote className="py-3 text-left">
                {indexes.data.reason ?? `${engine.label} keeps no statistics for an index.`}
              </EmptyNote>
            ) : (indexes.data.indexes ?? []).length === 0 ? (
              <EmptyNote className="py-3 text-left">This table has no index.</EmptyNote>
            ) : (
              <div className="-mx-4">
                <Table>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead className="h-8">Index</TableHead>
                      <TableHead className="h-8 text-right">Size</TableHead>
                      <TableHead className="h-8 text-right">Scans</TableHead>
                      <TableHead className="h-8 text-right max-md:hidden">Rows read</TableHead>
                      <TableHead className="h-8">Reading</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {indexes.data.indexes.map((index) => (
                      <TableRow key={index.name} className="hover:bg-transparent">
                        <TableCell className="py-1.5">
                          <span className="font-mono font-medium">{index.name}</span>
                          <p className="truncate font-mono text-hint text-muted-foreground">
                            {index.columns.join(", ")}
                          </p>
                        </TableCell>
                        <TableCell className="numeric py-1.5 text-right">
                          {bytes(index.bytes)}
                        </TableCell>
                        <TableCell className="numeric py-1.5 text-right">
                          {kept(index.scans) ? grouped(index.scans) : "not counted"}
                        </TableCell>
                        <TableCell className="numeric py-1.5 text-right max-md:hidden">
                          {kept(index.rowsRead) ? grouped(index.rowsRead) : ""}
                        </TableCell>
                        <TableCell className="py-1.5">
                          <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                            {index.primary ? (
                              <Tag>primary</Tag>
                            ) : index.unique ? (
                              <Tag>unique</Tag>
                            ) : null}
                            {!index.valid && <Tag tone="warning">not usable</Tag>}
                            {index.unused && <Tag tone="warning">never used</Tag>}
                            {index.duplicateOf && (
                              <Tag tone="warning" title={`Identical to ${index.duplicateOf}`}>
                                copy of {index.duplicateOf}
                              </Tag>
                            )}
                            {index.coveredBy && (
                              <Tag title={`${index.coveredBy} starts with the same columns`}>
                                covered by {index.coveredBy}
                              </Tag>
                            )}
                          </span>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
            {indexes.data?.notes?.map((note) => (
              <p key={note} className="pt-2 text-hint text-muted-foreground">
                {note}
              </p>
            ))}
          </section>
        )}
      </div>
    </div>
  )
}

function FiguresSkeleton() {
  return (
    <div aria-hidden className="space-y-4 pt-1">
      <Skeleton className="h-2 w-full rounded-full" />
      <div className="flex gap-8">
        {["w-14", "w-12", "w-16", "w-12"].map((width, index) => (
          <div key={index} className="space-y-2">
            <Skeleton className="h-2.5 w-10" />
            <Skeleton className={`h-3.5 ${width}`} />
          </div>
        ))}
      </div>
    </div>
  )
}

/** One bar divided by what it is made of, with what each part is under it. */
function Composition({
  label,
  total,
  parts,
}: {
  /** The bar's own reading: "3.2 MB on disk". */
  label: string
  total?: string
  parts: { key: string; label: string; value: number; figure: string; color: string }[]
}) {
  const shown = parts.filter((part) => part.value > 0)
  const sum = shown.reduce((all, part) => all + part.value, 0)
  if (sum === 0) return null
  return (
    <div className="min-w-0 space-y-2">
      <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <p className="numeric text-sm font-medium">{label}</p>
        {total && <p className="text-hint text-muted-foreground">{total}</p>}
      </div>
      {/* A chart, so it takes the series colours: no part of a table is a state. */}
      <div
        role="img"
        aria-label={shown.map((part) => `${part.label} ${part.figure}`).join(", ")}
        className="flex h-2 w-full overflow-hidden rounded-full bg-meter-track"
      >
        {shown.map((part) => (
          <span
            key={part.key}
            className="h-full"
            style={{ width: `${(part.value / sum) * 100}%`, backgroundColor: part.color }}
          />
        ))}
      </div>
      <ul className="flex flex-wrap gap-x-4 gap-y-1">
        {shown.map((part) => (
          <li key={part.key} className="flex min-w-0 items-center gap-1.5">
            <span
              aria-hidden
              className="size-1.5 shrink-0 rounded-full"
              style={{ backgroundColor: part.color }}
            />
            <span className="truncate text-hint text-muted-foreground">{part.label}</span>
            <span className="numeric text-hint">{part.figure}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function Figures({ stat, notes }: { stat: DbTableStat; notes?: string[] }) {
  const tended = [
    { label: "Vacuumed", at: latest(stat.lastVacuum, stat.lastAutovacuum) },
    { label: "Analyzed", at: latest(stat.lastAnalyze, stat.lastAutoanalyze) },
  ].filter((entry) => entry.at !== undefined)
  const writes = [stat.inserts, stat.updates, stat.deletes].every(kept)
  return (
    <div className="animate-rise space-y-5">
      <div className="grid gap-x-10 gap-y-5 lg:grid-cols-2">
        <Composition
          label={`${bytes(stat.totalBytes)} on disk`}
          total={
            kept(stat.bloatBytes) && stat.bloatBytes > 0
              ? `about ${bytes(stat.bloatBytes)} reclaimable, estimated`
              : undefined
          }
          parts={[
            {
              key: "table",
              label: "Rows",
              value: stat.tableBytes,
              figure: bytes(stat.tableBytes),
              color: "var(--chart-1)",
            },
            {
              key: "indexes",
              label: "Indexes",
              value: stat.indexBytes,
              figure: bytes(stat.indexBytes),
              color: "var(--chart-2)",
            },
            {
              key: "toast",
              label: "Large values",
              value: Math.max(stat.toastBytes, 0),
              figure: bytes(Math.max(stat.toastBytes, 0)),
              color: "var(--chart-4)",
            },
          ]}
        />
        {kept(stat.seqScans) && kept(stat.indexScans) && stat.seqScans + stat.indexScans > 0 && (
          <Composition
            label={`Read ${grouped(stat.seqScans + stat.indexScans)} times`}
            total="since the engine last reset its counters"
            parts={[
              {
                key: "index",
                label: "By an index",
                value: stat.indexScans,
                figure: grouped(stat.indexScans),
                color: "var(--chart-5)",
              },
              {
                key: "scan",
                label: "By scanning it whole",
                value: stat.seqScans,
                figure: grouped(stat.seqScans),
                color: "var(--chart-3)",
              },
            ]}
          />
        )}
      </div>
      <Facts>
        <Metric label="Rows" value={kept(stat.rows) ? `~${grouped(stat.rows)}` : "not counted"} />
        {kept(stat.deadRows) && <Metric label="Dead rows" value={grouped(stat.deadRows)} />}
        {writes && (
          <Metric
            label="Inserted · updated · deleted"
            value={`${grouped(stat.inserts)} · ${grouped(stat.updates)} · ${grouped(stat.deletes)}`}
          />
        )}
        {!writes && kept(stat.updates) && <Metric label="Writes" value={grouped(stat.updates)} />}
        {kept(stat.modsSinceAnalyze) && (
          <Metric label="Changed since analyzed" value={grouped(stat.modsSinceAnalyze)} />
        )}
        {stat.engine && <Metric label="Engine" value={stat.engine} />}
        {stat.parts !== undefined && <Metric label="Parts" value={grouped(stat.parts)} />}
        {tended.map((entry) => (
          <Metric
            key={entry.label}
            label={entry.label}
            value={<span title={timestamp(entry.at)}>{relativeTime(entry.at)}</span>}
          />
        ))}
      </Facts>
      {notes?.map((note) => (
        <p key={note} className="text-hint text-muted-foreground">
          {note}
        </p>
      ))}
    </div>
  )
}

/** The later of two times the engine may have kept. */
function latest(a: string | undefined, b: string | undefined): string | undefined {
  if (!a) return b
  if (!b) return a
  return new Date(a).getTime() >= new Date(b).getTime() ? a : b
}

type Run = { action: DbMaintenanceAction; result?: DbMaintenanceResult; error?: Error }

/**
 * The engine's maintenance for this one table. The list is the server's own
 * closed one, cut to the actions that take a table and to what the role may
 * run. One runs at a time, on a request held open until the engine is done;
 * what the engine printed is shown under the buttons, with the statement.
 */
function Maintenance({ detail, onRan }: { detail: DbTableDetail; onRan: () => void }) {
  const { id, engine } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const list = usePoll(
    (signal) =>
      get<{ actions?: DbMaintenanceAction[] }>(`/databases/${id}/maintenance`, undefined, signal),
    0,
    [id],
  )
  const [running, setRunning] = useState<DbMaintenanceAction | null>(null)
  const [last, setLast] = useState<Run>()
  const flight = useRef<AbortController | null>(null)
  useEffect(() => () => flight.current?.abort(), [])

  const actions = (list.data?.actions ?? []).filter(
    (action) => action.scope !== "database" && can(action.requires),
  )
  if (list.data && actions.length === 0) return null

  const run = async (action: DbMaintenanceAction) => {
    const controller = new AbortController()
    flight.current = controller
    setRunning(action)
    try {
      const result = await api<DbMaintenanceResult>(`/databases/${id}/maintenance`, {
        method: "POST",
        body: { action: action.id, schema: detail.schema || undefined, table: detail.name },
        signal: controller.signal,
      })
      setLast({ action, result })
      onRan()
    } catch (err) {
      if (controller.signal.aborted) {
        notify.info(`${action.label} was stopped`)
        return
      }
      setLast({ action, error: err instanceof Error ? err : new Error(String(err)) })
    } finally {
      if (flight.current === controller) setRunning(null)
    }
  }

  const press = (action: DbMaintenanceAction) => {
    if (!action.blocking && !action.destructive) return void run(action)
    confirm({
      title: action.label,
      confirmLabel: action.label,
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{qualified(detail.schema, detail.name)}</span>,
        facts: (
          <>
            {detail.estimatedRows >= 0 && (
              <FormFact label="Rows">about {grouped(detail.estimatedRows)}</FormFact>
            )}
            {detail.size !== undefined && <FormFact label="Size">{bytes(detail.size)}</FormFact>}
          </>
        ),
      },
      description: <p>{action.description}</p>,
      action: async () => {
        // The dialog closes on the press; the run reports under the buttons.
        void run(action)
        return "reported"
      },
    })
  }

  return (
    <section aria-label="Maintenance" className="min-w-0 space-y-3">
      <h3 className="border-b border-hairline pb-2 text-title font-medium">Maintenance</h3>
      {list.error && !list.data ? (
        <ReadFailed error={list.error} onRetry={list.refresh} />
      ) : !list.data ? (
        <Skeleton className="h-7 w-72 max-w-full" />
      ) : (
        <div className="flex flex-wrap items-center gap-1.5">
          {actions.map((action) => (
            <Button
              key={action.id}
              size="xs"
              variant="outline"
              className="max-sm:h-8"
              title={action.description}
              disabled={running !== null}
              onClick={() => press(action)}
            >
              {action.label}
              {(action.blocking || action.destructive) && "…"}
            </Button>
          ))}
        </div>
      )}
      <div aria-live="polite" className="space-y-3">
        {running && (
          <div className="flex flex-wrap items-center gap-3">
            <TextShimmer className="text-body">{`Running ${running.label.toLowerCase()} on ${detail.name}…`}</TextShimmer>
            <Button size="xs" variant="ghost" onClick={() => flight.current?.abort()}>
              Stop
            </Button>
          </div>
        )}
        {!running && last?.error && (
          <p role="alert" className="text-body break-words text-destructive">
            {last.action.label} did not run.{" "}
            {last.error instanceof ApiError ? last.error.message : String(last.error)}
          </p>
        )}
        {!running && last?.result && (
          <div className="animate-rise space-y-3">
            <p className="text-body">
              {last.action.label} {last.result.ok ? "finished" : "ran and reported a problem"}
              <span className="text-muted-foreground">
                {" "}
                in {readableDuration(last.result.duration)}
              </span>
            </p>
            <Statement sql={last.result.statements.join(";\n")} placeholder="" />
            {last.result.output.length > 0 && (
              <Well className="max-h-64 overflow-auto text-hint leading-relaxed whitespace-pre-wrap">
                {last.result.output.join("\n")}
              </Well>
            )}
          </div>
        )}
      </div>
      {dialog}
    </section>
  )
}
