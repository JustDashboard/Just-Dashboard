"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { CodeBracket, GridSquare, Wrench } from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes, percent, relativeTime, timestamp } from "@/lib/format"
import { useViewState } from "@/lib/view-state"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { FormFact, FormFacts, FormNote } from "@/components/form"
import { Detail, DetailList, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, LoadingPanel } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { VerbMenu } from "@/components/verbs"
import { KindGlyph, rowObjectKind } from "@/components/database/data/kinds"
import { compact } from "@/components/database/home/readings"
import { STORAGE_LIMIT, readTableStats } from "@/components/database/ops/performance-api"
import {
  MaintenanceList,
  useMaintenance,
  type Maintenance,
} from "@/components/database/ops/performance-maintenance"
import {
  NoFigure,
  NotAvailable,
  Notes,
  ObjectName,
  ScopeChips,
  SortHead,
  Stale,
  ViewRead,
} from "@/components/database/ops/performance-parts"
import {
  compressionRatio,
  counted,
  deadShare,
  isTableSort,
  lastRun,
  needsVacuum,
  neverAnalysed,
  objectParam,
  parseObject,
  qualified,
  schemasOf,
  seqShare,
  sortTables,
  tableColumns,
  type TableColumn,
  type TableSort,
} from "@/components/database/ops/performance-storage"
import type { DbTableStat } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** What a table's bytes are, in the colours of the bar that draws them. */
const PARTS = [
  { key: "tableBytes", label: "Rows", color: "var(--chart-1)" },
  { key: "indexBytes", label: "Indexes", color: "var(--chart-2)" },
  { key: "toastBytes", label: "Large values", color: "var(--chart-4)" },
] as const

/**
 * What each table weighs and how it is being kept: its rows and its size by
 * what the size is made of, the dead rows a vacuum would clear, the space it
 * holds and does not use, how it is read, and when it was last vacuumed and
 * analysed — with the engine's own maintenance on each row.
 *
 * An engine fills the columns it keeps figures for, and the rest are not
 * drawn: a count the engine does not keep is never a zero. The list is every
 * schema's unless the reader narrows it, and the chips that narrow it are on
 * the page, so nothing is filtered by a place the page does not show.
 *
 * A table whose dead rows have passed the line the advisor draws, or that has
 * rows and has never been analysed, says so on its row.
 */
export function TablesView() {
  const { id, engine, param, select } = useDatabase()
  const wide = useMediaQuery("(min-width: 900px)")
  const roomy = useMediaQuery("(min-width: 1500px)")
  const stats = usePoll((signal) => readTableStats(id, signal), 60_000, [id])
  const maintenance = useMaintenance(stats.refresh)
  const [filter, setFilter] = useState("")
  const [order, setOrder] = useViewState<{ by: string; descending: boolean }>(
    `databases.${id}.performance.tables.order`,
    { by: "size", descending: true },
  )
  const by: TableSort = isTableSort(order.by) ? order.by : "size"
  const scope = param("scope")
  const objects = engine.nouns.objects
  const title = objects[0].toUpperCase() + objects.slice(1)
  const noun = engine.nouns.object[0].toUpperCase() + engine.nouns.object.slice(1)

  const all = stats.data?.tables
  const schemas = useMemo(() => schemasOf(all ?? []), [all])
  const columns = useMemo(() => tableColumns(all ?? []), [all])
  const largest = useMemo(
    () => (all ?? []).reduce((most, table) => Math.max(most, table.totalBytes), 0),
    [all],
  )
  const rows = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const inScope = (all ?? []).filter(
      (table) =>
        (!scope || table.schema === scope) &&
        (!needle || qualified(table.schema, table.table).toLowerCase().includes(needle)),
    )
    return sortTables(inScope, by, order.descending)
  }, [all, scope, filter, by, order.descending])

  const sort = (next: TableSort) =>
    setOrder({ by: next, descending: next === by ? !order.descending : next !== "name" })
  const head = (label: string, column: TableSort, className?: string) => (
    <SortHead
      label={label}
      active={by === column}
      descending={order.descending}
      onSort={() => sort(column)}
      align={column === "name" ? "left" : "right"}
      className={className}
    />
  )

  const opened = parseObject(param("object"))
  const open = opened
    ? all?.find((table) => table.schema === opened.schema && table.table === opened.name)
    : undefined
  const openTable = (table: DbTableStat) =>
    select({ object: objectParam(table.schema, table.table) })
  const whole = maintenance.verbsFor({})
  const toast = (all ?? []).some((table) => table.toastBytes > 0)

  return (
    <Panel plain aria-label={title}>
      {maintenance.dialogs}
      <PanelHeader
        title={title}
        actions={
          <>
            <Stale poll={stats} />
            <SearchInput
              dense
              aria-label={`Filter the ${objects}`}
              placeholder="Filter by name"
              value={filter}
              containerClassName="sm:w-52"
              onChange={(event) => setFilter(event.target.value)}
            />
            {whole.length > 0 && (
              <VerbMenu
                verbs={whole}
                trigger={
                  <Button size="sm" variant="outline">
                    <Wrench />
                    Maintain database
                  </Button>
                }
              />
            )}
          </>
        }
      />
      <ViewRead poll={stats} what={`the ${objects}`} skeleton={<LoadingPanel plain rows={8} />}>
        {(data) =>
          !data.supported ? (
            <div className="pt-4">
              <NotAvailable
                title={`The ${engine.nouns.object} statistics cannot be read`}
                reason={data.reason}
              />
            </div>
          ) : (
            <div className="animate-rise space-y-3 pt-3">
              <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-6 gap-y-2">
                <ScopeChips
                  schemas={schemas}
                  scope={scope}
                  onScope={(schema) => select({ scope: schema || null })}
                  noun={engine.nouns.containers}
                />
                {largest > 0 && (
                  <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
                    {PARTS.filter((part) => part.key !== "toastBytes" || toast).map((part) => (
                      <span key={part.key} className="flex items-center gap-1.5">
                        <span
                          aria-hidden
                          className="size-1.5 rounded-full"
                          style={{ background: part.color }}
                        />
                        {part.label}
                      </span>
                    ))}
                  </p>
                )}
              </div>
              <PanelBody flush className="group-data-[plain]/panel:-mx-4">
                {data.tables.length === 0 ? (
                  <EmptyNote className="px-4">
                    This database holds no {objects} yet. Create one on the Schema page, or import a
                    file on the Data page.
                  </EmptyNote>
                ) : rows.length === 0 ? (
                  <EmptyNote className="px-4">
                    No {engine.nouns.object} matches {filter.trim() || scope}.
                  </EmptyNote>
                ) : wide ? (
                  <Table>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        {head(noun, "name")}
                        {head("Rows", "rows", "px-2")}
                        {head("Size", "size", "px-2")}
                        {columns.has("dead") && head("Dead rows", "dead", "px-2")}
                        {columns.has("bloat") && head("Unused space", "bloat", "px-2")}
                        {columns.has("scans") && roomy && head("Sequential scans", "scans", "px-2")}
                        {columns.has("parts") && (
                          <TableHead className="px-2 text-right">Parts</TableHead>
                        )}
                        {columns.has("compression") && (
                          <TableHead className="px-2 text-right">Compressed</TableHead>
                        )}
                        {columns.has("vacuum") && <TableHead className="px-2">Vacuumed</TableHead>}
                        {columns.has("analyze") && <TableHead className="px-2">Analysed</TableHead>}
                        <TableHead>
                          <span className="sr-only">Actions</span>
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((table) => (
                        <TableRow
                          key={objectParam(table.schema, table.table)}
                          data-state={open === table ? "selected" : undefined}
                        >
                          <TableCell className="max-w-72 py-2">
                            <span className="flex min-w-0 items-center gap-2">
                              <KindGlyph kind={rowObjectKind(table.kind)} />
                              <RowLink mono onClick={() => openTable(table)}>
                                <span className="sr-only">Open </span>
                                <ObjectName
                                  schema={schemas.length > 1 ? table.schema : undefined}
                                  name={table.table}
                                />
                              </RowLink>
                              {table.engine && <Tag mono>{table.engine}</Tag>}
                              {table.kind && table.kind !== "table" && <Tag>{table.kind}</Tag>}
                            </span>
                          </TableCell>
                          <TableCell className="numeric px-2 py-2 text-right">
                            {table.rows < 0 ? <NoFigure /> : compact(table.rows)}
                          </TableCell>
                          <TableCell className="px-2 py-2">
                            <SizeBar table={table} largest={largest} />
                          </TableCell>
                          {columns.has("dead") && (
                            <TableCell className="px-2 py-2 text-right">
                              <DeadRows table={table} />
                            </TableCell>
                          )}
                          {columns.has("bloat") && (
                            <TableCell className="numeric px-2 py-2 text-right text-muted-foreground">
                              {counted(table.bloatBytes) === undefined ? (
                                <NoFigure />
                              ) : (
                                bytes(table.bloatBytes)
                              )}
                            </TableCell>
                          )}
                          {columns.has("scans") && roomy && (
                            <TableCell className="px-2 py-2 text-right">
                              <Scans table={table} />
                            </TableCell>
                          )}
                          {columns.has("parts") && (
                            <TableCell className="numeric px-2 py-2 text-right">
                              {table.parts === undefined ? <NoFigure /> : compact(table.parts)}
                            </TableCell>
                          )}
                          {columns.has("compression") && (
                            <TableCell className="numeric px-2 py-2 text-right text-muted-foreground">
                              <Compression table={table} />
                            </TableCell>
                          )}
                          {columns.has("vacuum") && (
                            <TableCell className="px-2 py-2">
                              <Kept run={lastRun(table.lastVacuum, table.lastAutovacuum)} />
                            </TableCell>
                          )}
                          {columns.has("analyze") && (
                            <TableCell className="px-2 py-2">
                              {neverAnalysed(table, columns) ? (
                                <span className="text-warning">never</span>
                              ) : (
                                <Kept run={lastRun(table.lastAnalyze, table.lastAutoanalyze)} />
                              )}
                            </TableCell>
                          )}
                          <TableCell className="py-1 text-right">
                            <TableVerbs table={table} maintenance={maintenance} />
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                ) : (
                  <ul className="divide-y divide-hairline border-y border-hairline">
                    {rows.map((table) => (
                      <li
                        key={objectParam(table.schema, table.table)}
                        className="space-y-1.5 px-4 py-2.5"
                      >
                        <div className="flex min-w-0 items-center gap-2">
                          <KindGlyph kind={rowObjectKind(table.kind)} />
                          <RowLink mono className="flex-1" onClick={() => openTable(table)}>
                            <span className="sr-only">Open </span>
                            <ObjectName
                              schema={schemas.length > 1 ? table.schema : undefined}
                              name={table.table}
                            />
                          </RowLink>
                          <TableVerbs table={table} maintenance={maintenance} />
                        </div>
                        <SizeBar table={table} largest={largest} />
                        <p className="flex min-w-0 flex-wrap gap-x-3 gap-y-0.5 text-hint text-muted-foreground">
                          {table.rows >= 0 && <span>{compact(table.rows)} rows</span>}
                          {needsVacuum(table) && (
                            <span className="text-warning">
                              {compact(table.deadRows)} dead rows
                            </span>
                          )}
                          {neverAnalysed(table, columns) && (
                            <span className="text-warning">never analysed</span>
                          )}
                        </p>
                      </li>
                    ))}
                  </ul>
                )}
              </PanelBody>
              {data.truncated && (
                <FormNote>
                  The {STORAGE_LIMIT} largest are listed; this database holds more {objects} than
                  that.
                </FormNote>
              )}
              {columns.has("bloat") && (
                <FormNote>
                  Unused space is what a table holds and is not using. Where the engine has to work
                  it out from its statistics it is an estimate, as fresh as the last analyse.
                </FormNote>
              )}
              <Notes notes={data.notes} />
            </div>
          )
        }
      </ViewRead>

      {open && (
        <TablePanel
          table={open}
          columns={columns}
          maintenance={maintenance}
          onClose={() => select({ object: null })}
        />
      )}
    </Panel>
  )
}

/** A table's size, and what it is made of, against the largest table on the page. */
function SizeBar({ table, largest }: { table: DbTableStat; largest: number }) {
  const width = largest > 0 ? (table.totalBytes / largest) * 100 : 0
  const parts = PARTS.map((part) => ({ ...part, bytes: Math.max(table[part.key], 0) })).filter(
    (part) => part.bytes > 0,
  )
  const sum = parts.reduce((total, part) => total + part.bytes, 0)
  return (
    <span className="flex min-w-36 items-center justify-end gap-2.5">
      <span
        role="img"
        aria-label={parts.map((part) => `${part.label} ${bytes(part.bytes)}`).join(", ")}
        className="relative h-1 min-w-12 flex-1 overflow-hidden rounded-full bg-meter-track"
      >
        <span
          className="absolute inset-y-0 left-0 flex gap-px"
          style={{ width: `${Math.max(width, 2)}%` }}
        >
          {parts.map((part) => (
            <span
              key={part.key}
              title={`${part.label} · ${bytes(part.bytes)}`}
              className="h-full first:rounded-l-full last:rounded-r-full"
              style={{ width: `${(part.bytes / sum) * 100}%`, background: part.color }}
            />
          ))}
        </span>
      </span>
      <span className="numeric w-16 shrink-0 text-right">{bytes(table.totalBytes)}</span>
    </span>
  )
}

function DeadRows({ table }: { table: DbTableStat }) {
  const dead = counted(table.deadRows)
  if (dead === undefined) return <NoFigure />
  const share = deadShare(table) ?? 0
  const due = needsVacuum(table)
  return (
    <span
      className={cn("numeric", due ? "font-medium text-warning" : "text-muted-foreground")}
      title={due ? "More than a fifth of this table is dead rows: a vacuum is due" : undefined}
    >
      {compact(dead)}
      {dead > 0 && (
        <span className="text-hint"> · {percent(share * 100, share < 0.1 ? 1 : 0)}</span>
      )}
    </span>
  )
}

/** How a table is read: its sequential scans, and what share of all its scans they are. */
function Scans({ table }: { table: DbTableStat }) {
  const seq = counted(table.seqScans)
  if (seq === undefined) return <NoFigure />
  const share = seqShare(table)
  return (
    <span className="numeric text-muted-foreground">
      {compact(seq)}
      {share !== undefined && <span className="text-hint"> · {percent(share * 100, 0)}</span>}
    </span>
  )
}

function Compression({ table }: { table: DbTableStat }) {
  const ratio = compressionRatio(table)
  return ratio === undefined ? <NoFigure /> : <>{ratio.toFixed(1)}×</>
}

/** When something was last done to a table, and whether the engine did it by itself. */
function Kept({ run }: { run: ReturnType<typeof lastRun> }) {
  if (!run) return <span className="text-muted-foreground/60">never</span>
  return (
    <span className="text-muted-foreground" title={timestamp(run.at)}>
      {relativeTime(run.at)}
      {run.automatic && <span className="text-hint"> · auto</span>}
    </span>
  )
}

function TableVerbs({ table, maintenance }: { table: DbTableStat; maintenance: Maintenance }) {
  const verbs = maintenance.verbsFor({ schema: table.schema, table: table.table })
  if (verbs.length === 0) return null
  return <VerbMenu verbs={verbs} label={`Maintain ${qualified(table.schema, table.table)}`} />
}

/**
 * One table, opened: every figure the engine keeps about it, the ways into
 * its rows and its definition, and its maintenance — each action with the
 * engine's own sentence about what it does and what it locks.
 */
function TablePanel({
  table,
  columns,
  maintenance,
  onClose,
}: {
  table: DbTableStat
  columns: ReadonlySet<TableColumn>
  maintenance: Maintenance
  onClose: () => void
}) {
  const { engine, href } = useDatabase()
  const verbs = maintenance.forTarget({ schema: table.schema, table: table.table })
  const figure = (value: number | undefined) =>
    counted(value) === undefined ? <NoFigure /> : (value as number).toLocaleString()
  const when = (at: string | undefined) =>
    at ? (
      <span title={timestamp(at)}>{relativeTime(at)}</span>
    ) : (
      <span className="text-muted-foreground">never</span>
    )
  const ratio = compressionRatio(table)
  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="md"
      initialFocus="body"
      title={
        <>
          <KindGlyph kind={rowObjectKind(table.kind)} />
          <ObjectName schema={table.schema} name={table.table} className="text-title" />
        </>
      }
      description={`What ${engine.label} keeps about this ${engine.nouns.object}, and its maintenance.`}
      actions={
        <>
          <Button size="sm" variant="outline" asChild>
            <Link href={href("data", { schema: table.schema, table: table.table })}>
              <GridSquare />
              Open in Data
            </Link>
          </Button>
          {engine.has("schema") && (
            <Button size="sm" variant="outline" asChild>
              <Link href={href("schema", { schema: table.schema, table: table.table })}>
                <CodeBracket />
                Open in Schema
              </Link>
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-5">
        <FormFacts>
          <FormFact label="Rows">{table.rows < 0 ? "not counted" : compact(table.rows)}</FormFact>
          <FormFact label="Size">{bytes(table.totalBytes)}</FormFact>
          {table.engine && (
            <FormFact label="Engine" mono>
              {table.engine}
            </FormFact>
          )}
        </FormFacts>

        <section className="space-y-2">
          <p className="eyebrow">Size</p>
          <DetailList>
            <Detail label="Rows">{bytes(table.tableBytes)}</Detail>
            <Detail label="Indexes">{bytes(table.indexBytes)}</Detail>
            {table.toastBytes > 0 && (
              <Detail label="Large values">{bytes(table.toastBytes)}</Detail>
            )}
            {counted(table.bloatBytes) !== undefined && (
              <Detail label="Unused, estimated">{bytes(table.bloatBytes)}</Detail>
            )}
            {ratio !== undefined && (
              <Detail label="Before compression">
                {bytes(table.uncompressedBytes)} · {ratio.toFixed(1)}× smaller on disk
              </Detail>
            )}
            {table.parts !== undefined && (
              <Detail label="Parts">{table.parts.toLocaleString()}</Detail>
            )}
          </DetailList>
        </section>

        {(columns.has("dead") || counted(table.inserts) !== undefined) && (
          <section className="space-y-2">
            <p className="eyebrow">Rows</p>
            <DetailList>
              {columns.has("dead") && (
                <Detail label="Dead">
                  <DeadRows table={table} />
                </Detail>
              )}
              {counted(table.modsSinceAnalyze) !== undefined && (
                <Detail label="Changed since analysed">{figure(table.modsSinceAnalyze)}</Detail>
              )}
              {counted(table.inserts) !== undefined && (
                <Detail label="Inserted">{figure(table.inserts)}</Detail>
              )}
              {counted(table.updates) !== undefined && (
                <Detail label="Updated">{figure(table.updates)}</Detail>
              )}
              {counted(table.deletes) !== undefined && (
                <Detail label="Deleted">{figure(table.deletes)}</Detail>
              )}
            </DetailList>
          </section>
        )}

        {columns.has("scans") && (
          <section className="space-y-2">
            <p className="eyebrow">How it is read</p>
            <DetailList>
              <Detail label="Sequential scans">
                {figure(table.seqScans)}
                {counted(table.seqRowsRead) !== undefined && (
                  <span className="text-muted-foreground">
                    {" "}
                    · {compact(table.seqRowsRead)} rows read
                  </span>
                )}
              </Detail>
              <Detail label="Index scans">
                {figure(table.indexScans)}
                {counted(table.indexRowsRead) !== undefined && (
                  <span className="text-muted-foreground">
                    {" "}
                    · {compact(table.indexRowsRead)} rows read
                  </span>
                )}
              </Detail>
            </DetailList>
          </section>
        )}

        {(columns.has("vacuum") || columns.has("analyze")) && (
          <section className="space-y-2">
            <p className="eyebrow">Housekeeping</p>
            <DetailList>
              {columns.has("vacuum") && (
                <>
                  <Detail label="Vacuumed by hand">{when(table.lastVacuum)}</Detail>
                  <Detail label="Vacuumed by the engine">{when(table.lastAutovacuum)}</Detail>
                </>
              )}
              {columns.has("analyze") && (
                <>
                  <Detail label="Analysed by hand">{when(table.lastAnalyze)}</Detail>
                  <Detail label="Analysed by the engine">{when(table.lastAutoanalyze)}</Detail>
                </>
              )}
            </DetailList>
          </section>
        )}

        {verbs.length > 0 && (
          <section className="space-y-2">
            <p className="eyebrow">Maintenance</p>
            <MaintenanceList
              verbs={verbs}
              maintenance={maintenance}
              on={qualified(table.schema, table.table)}
            />
          </section>
        )}
      </div>
    </SidePanel>
  )
}
