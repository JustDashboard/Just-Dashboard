"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { ChevronDown } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { FormFact, Statement } from "@/components/form"
import { Metric } from "@/components/page"
import { Well } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
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
import { useFocusReturn } from "@/components/database/schema/focus"
import {
  maintenanceKey,
  startMaintenance,
  stopMaintenance,
  useMaintenance,
  useMaintenanceReader,
} from "@/components/database/schema/maintenance-runs"
import {
  elapsed,
  maintenanceVariants,
  type MaintenanceVariant,
} from "@/components/database/schema/maintenance-variants"
import { quietNotes } from "@/components/database/schema/stat-notes"
import type {
  DbIndexStat,
  DbIndexStats,
  DbMaintenanceAction,
  DbTableDetail,
  DbTableStat,
  DbTableStats,
} from "@/components/database/schema/types"

/** How many tables are asked for: the server's ceiling, so the one wanted is among them. */
const MOST = 1000
/** Below this the index figures are read down, an index to a block, instead of across. */
const NARROW = 560

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
  asked,
}: {
  detail: DbTableDetail
  /** The connection takes changes here: not protected, and a schema of the reader's own. */
  mayRun: boolean
  /** Counts the times the reader asked for the schema to be read again. */
  asked: number
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
  const refreshStats = stats.refresh
  const refreshIndexes = indexes.refresh
  const refresh = useCallback(() => {
    refreshStats()
    refreshIndexes()
  }, [refreshStats, refreshIndexes])
  // Asked again from the tree: the figures are read again with the rest.
  const answered = useRef(asked)
  useEffect(() => {
    if (answered.current === asked) return
    answered.current = asked
    refresh()
  }, [asked, refresh])
  // What the engine could not say, once, under everything it could.
  const notes = quietNotes(stats.data?.notes, indexes.data?.notes)
  // A size the engine did not give is 0 on the wire: an index is never no
  // bytes, so where none has a size the column says nothing at all.
  const listed = indexes.data?.indexes ?? []
  const sized = listed.some((index) => index.bytes > 0)
  // The same of the counters: where the engine counts for none of them, a
  // column of "not counted" says once what the line under the table says.
  const counted = listed.some((index) => kept(index.scans))
  const followed = listed.some((index) => kept(index.rowsRead))

  // Across while there is room for the figures, down when there is not.
  const [box, width] = useColumnWidth<HTMLDivElement>()
  const narrow = width > 0 && width < NARROW

  return (
    <div ref={box} className="min-h-0 flex-1 overflow-y-auto">
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
            <Figures stat={stat} />
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
              <IndexUse
                indexes={indexes.data.indexes}
                sized={sized}
                counted={counted}
                followed={followed}
                narrow={narrow}
                keyWord={engine.capabilities.rowIdentity === "none" ? "sorting key" : "primary"}
              />
            )}
          </section>
        )}

        {notes.length > 0 && (
          <p className="max-w-3xl text-hint leading-relaxed text-muted-foreground">
            {notes.join(" ")}
          </p>
        )}
      </div>
    </div>
  )
}

/** What each index costs and earns: one row to an index, read across or — on a narrow pane — down. */
function IndexUse({
  indexes,
  sized,
  counted,
  followed,
  narrow,
  keyWord,
}: {
  indexes: DbIndexStat[]
  /** The engine gave sizes, scan counts, rows read: a figure it gave for none has no column. */
  sized: boolean
  counted: boolean
  followed: boolean
  narrow: boolean
  /** What this engine calls its key. */
  keyWord: string
}) {
  const reading = (index: DbIndexStat) => (
    <>
      {index.primary ? <Tag>{keyWord}</Tag> : index.unique ? <Tag>unique</Tag> : null}
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
    </>
  )
  if (narrow) {
    return (
      <ul aria-label="Indexes" className="divide-y divide-hairline">
        {indexes.map((index) => {
          const figures = [
            sized && index.bytes > 0 && bytes(index.bytes),
            counted && (kept(index.scans) ? `${grouped(index.scans)} scans` : "scans not counted"),
            followed && kept(index.rowsRead) && `${grouped(index.rowsRead)} rows read`,
          ].filter(Boolean)
          return (
            <li key={index.name} className="space-y-1 py-2">
              <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1">
                <span className="font-mono text-xs font-medium break-all">{index.name}</span>
                {reading(index)}
              </div>
              <p className="font-mono text-hint break-words text-muted-foreground">
                {index.columns.join(", ")}
              </p>
              {figures.length > 0 && <p className="numeric text-xs">{figures.join(" · ")}</p>}
            </li>
          )
        })}
      </ul>
    )
  }
  return (
    <div className="-mx-4">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="h-8">Index</TableHead>
            {sized && <TableHead className="h-8 text-right">Size</TableHead>}
            {counted && <TableHead className="h-8 text-right">Scans</TableHead>}
            {followed && <TableHead className="h-8 text-right">Rows read</TableHead>}
            <TableHead className="h-8">Reading</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {indexes.map((index) => (
            <TableRow key={index.name} className="hover:bg-transparent">
              <TableCell className="py-1.5">
                <span className="font-mono font-medium">{index.name}</span>
                <p className="truncate font-mono text-hint text-muted-foreground">
                  {index.columns.join(", ")}
                </p>
              </TableCell>
              {sized && (
                <TableCell className="numeric py-1.5 text-right">
                  {index.bytes > 0 ? bytes(index.bytes) : ""}
                </TableCell>
              )}
              {counted && (
                <TableCell className="numeric py-1.5 text-right">
                  {kept(index.scans) ? grouped(index.scans) : "not counted"}
                </TableCell>
              )}
              {followed && (
                <TableCell className="numeric py-1.5 text-right">
                  {kept(index.rowsRead) ? grouped(index.rowsRead) : ""}
                </TableCell>
              )}
              <TableCell className="py-1.5">
                <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                  {reading(index)}
                </span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
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
export function Composition({
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

function Figures({ stat }: { stat: DbTableStat }) {
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
    </div>
  )
}

/** The later of two times the engine may have kept. */
function latest(a: string | undefined, b: string | undefined): string | undefined {
  if (!a) return b
  if (!b) return a
  return new Date(a).getTime() >= new Date(b).getTime() ? a : b
}

/**
 * The engine's maintenance for this one table. The list is the server's own
 * closed one, cut to the actions that take a table and to what the role may
 * run; an action that can be asked for in more than one way (a reindex that
 * blocks writes or one that does not) opens a short menu of them.
 *
 * One runs at a time, on a request held open until the engine is done. The
 * run is the table's, not this panel's (`maintenance-runs.ts`): it goes on
 * when the reader looks at another reading, and what the engine printed is
 * here, with the statement, when they come back.
 */
function Maintenance({ detail, onRan }: { detail: DbTableDetail; onRan: () => void }) {
  const { id, engine } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  useFocusReturn((dialog.props as { request: unknown }).request !== null)
  const list = usePoll(
    (signal) =>
      get<{ actions?: DbMaintenanceAction[] }>(`/databases/${id}/maintenance`, undefined, signal),
    0,
    [id],
  )
  const key = maintenanceKey(id, detail.schema, detail.name)
  const { run, outcome } = useMaintenance(key)
  useMaintenanceReader(key)

  // A run that landed changed the figures above: they are read again, once.
  const landed = outcome?.result ? outcome.at : 0
  const seen = useRef(landed)
  useEffect(() => {
    if (seen.current === landed) return
    seen.current = landed
    if (landed) onRan()
  }, [landed, onRan])

  const actions = (list.data?.actions ?? []).filter(
    (action) => action.scope !== "database" && can(action.requires),
  )
  if (list.data && actions.length === 0) return null

  const start = (action: DbMaintenanceAction, variant: MaintenanceVariant) =>
    startMaintenance({
      id,
      schema: detail.schema,
      table: detail.name,
      action,
      label: variant.label,
      options: variant.options,
    })

  const press = (action: DbMaintenanceAction, variant: MaintenanceVariant) => {
    if (!action.blocking && !action.destructive) return start(action, variant)
    confirm({
      title: variant.label,
      confirmLabel: variant.label,
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
      description: (
        <>
          <p>{action.description}</p>
          {variant.note && <p>{variant.note}</p>}
        </>
      ),
      action: async () => {
        // The dialog closes on the press; the run reports in the table's head.
        start(action, variant)
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
          {actions.map((action) => {
            const variants = maintenanceVariants(action)
            const asks = action.blocking || action.destructive
            const going = run?.action.id === action.id
            if (variants.length === 1) {
              return (
                <Button
                  key={action.id}
                  size="xs"
                  variant="outline"
                  className="max-sm:h-8"
                  title={action.description}
                  disabled={run !== undefined}
                  pending={going}
                  onClick={() => press(action, variants[0])}
                >
                  {action.label}
                  {asks && "…"}
                </Button>
              )
            }
            return (
              <DropdownMenu key={action.id}>
                <DropdownMenuTrigger asChild>
                  <Button
                    size="xs"
                    variant="outline"
                    className="max-sm:h-8"
                    title={action.description}
                    disabled={run !== undefined}
                  >
                    {action.label}
                    <ChevronDown className="text-muted-foreground" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="min-w-48">
                  {variants.map((variant) => (
                    <DropdownMenuItem
                      key={variant.key}
                      title={variant.note}
                      onSelect={() => press(action, variant)}
                    >
                      {variant.label}
                      {asks && "…"}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            )
          })}
        </div>
      )}
      <div aria-live="polite" className="space-y-3">
        {!run && outcome?.error && (
          <p role="alert" className="text-body break-words text-destructive">
            {outcome.label} did not run.{" "}
            {outcome.error instanceof ApiError ? outcome.error.message : String(outcome.error)}
          </p>
        )}
        {!run && outcome?.result && (
          <div className="animate-rise space-y-3">
            <p className="text-body">
              {outcome.label} {outcome.result.ok ? "finished" : "ran and reported a problem"}
              <span className="text-muted-foreground">
                {" "}
                in {readableDuration(outcome.result.duration)}
              </span>
            </p>
            <Statement sql={outcome.result.statements.join(";\n")} placeholder="" />
            {outcome.result.output.length > 0 && (
              <Well className="max-h-64 overflow-auto text-hint leading-relaxed whitespace-pre-wrap">
                {outcome.result.output.join("\n")}
              </Well>
            )}
          </div>
        )}
      </div>
      {dialog}
    </section>
  )
}

/** The time now, ticking each second while something is being timed. */
function useNow(ticking: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!ticking) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [ticking])
  return now
}

/**
 * A table's maintenance command in flight, said in the table's head whichever
 * reading is open: what is running, for how long, and the one way to end it.
 */
export function MaintenanceBand({ schema, table }: { schema: string; table: string }) {
  const { id } = useDatabase()
  const key = maintenanceKey(id, schema, table)
  const { run } = useMaintenance(key)
  const now = useNow(run !== undefined)
  if (!run) return null
  return (
    <div
      role="status"
      data-slot="maintenance-band"
      className="relative flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hairline px-4 py-1.5"
    >
      <TextShimmer className="text-body">{`${run.label} is running on ${table}`}</TextShimmer>
      <span className="numeric text-hint text-muted-foreground">
        {elapsed(Math.max(now, run.startedAt) - run.startedAt)}
      </span>
      <span className="min-w-0 truncate text-hint text-muted-foreground max-md:hidden">
        {run.action.blocking
          ? "The table is locked until it is done."
          : "It goes on while you read elsewhere."}
      </span>
      <Button
        size="xs"
        variant="outline"
        className="ml-auto max-sm:h-8"
        onClick={() => stopMaintenance(key)}
      >
        Stop
      </Button>
      {/* Working, and it cannot say how far. */}
      <span aria-hidden className="absolute inset-x-0 bottom-0 h-px overflow-hidden">
        <span className="absolute inset-y-0 left-0 w-1/3 animate-sweep bg-brand" />
      </span>
    </div>
  )
}
