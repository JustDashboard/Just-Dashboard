"use client"

import { useEffect, useMemo } from "react"
import { useRouter } from "next/navigation"
import { Wrench } from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes, percent, relativeTime, timestamp } from "@/lib/format"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { InfoTip } from "@/components/form"
import { RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SplitBar } from "@/components/rank-list"
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
import { tableHref } from "@/components/database/ops/performance-address"
import { STORAGE_LIMIT, readTableStats } from "@/components/database/ops/performance-api"
import {
  MaintenanceUnread,
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
  RETURNS_FOCUS,
} from "@/components/database/ops/performance-parts"
import {
  columnWidth,
  compressionRatio,
  counted,
  deadShare,
  fittedColumns,
  isTableSort,
  lastRun,
  needsVacuum,
  neverAnalysed,
  objectParam,
  qualified,
  schemasOf,
  seqShare,
  sortTables,
  tableColumns,
  type TableSort,
} from "@/components/database/ops/performance-storage"
import {
  TABLE_PARTS,
  tableStatsKey,
  tablesFilterKey,
  tablesOrderKey,
} from "@/components/database/ops/performance-lists"
import { Fig, ShareCell, ShareHead, TitleFact } from "@/components/database/ops/performance-words"
import type { DbTableStat } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

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
 *
 * A row's size is a figure and its share of the database beside it, in two
 * columns that line up down the list. Until 0.8.7 each row drew a bar of its
 * own, as long as the table was large and split by what its bytes were; what
 * the database's bytes are is one strip over the list now, and a table's own
 * split is on its page (`performance-table`), which the row's name opens.
 */
export function TablesView() {
  const { id, engine, param, select } = useDatabase()
  const router = useRouter()
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const stats = usePoll((signal) => readTableStats(id, signal), 60_000, [id], {
    key: tableStatsKey(id),
  })
  const maintenance = useMaintenance(stats.refresh)
  // The filter and the order are kept under the keys a table's page reads:
  // the rail beside a table is this list as it was left.
  const [filter, setFilter] = useSessionState(tablesFilterKey(id), "")
  const [order, setOrder] = useViewState<{ by: string; descending: boolean }>(tablesOrderKey(id), {
    by: "size",
    descending: true,
  })
  const by: TableSort = isTableSort(order.by) ? order.by : "size"
  const objects = engine.nouns.objects
  const title = objects[0].toUpperCase() + objects.slice(1)
  const noun = engine.nouns.object[0].toUpperCase() + engine.nouns.object.slice(1)

  const all = stats.data?.tables
  const schemas = useMemo(() => schemasOf(all ?? []), [all])
  // A schema the address names and the list does not hold narrows nothing:
  // the list is every schema's, and the address is put right.
  const asked = param("scope")
  const scope = all && !schemas.some((schema) => schema.name === asked) ? "" : asked
  useEffect(() => {
    if (asked && !scope) select({ scope: null })
  }, [asked, scope, select])

  const every = useMemo(() => tableColumns(all ?? []), [all])
  // Which shape, and which columns: by the width of the view itself, chosen
  // once and drawn once.
  const columns = fittedColumns(every, width)
  // What the database's bytes are, summed over every table listed: the one
  // strip the list draws, and what a row's share is a share of.
  const split = useMemo(
    () =>
      TABLE_PARTS.map((part) => ({
        ...part,
        bytes: (all ?? []).reduce((sum, table) => sum + Math.max(table[part.key], 0), 0),
      })),
    [all],
  )
  const whole = useMemo(
    () => (all ?? []).reduce((sum, table) => sum + Math.max(table.totalBytes, 0), 0),
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

  const openTable = (table: DbTableStat) =>
    router.push(tableHref(id, table.schema, table.table, { scope: scope || null }))
  const database = maintenance.verbsFor({})
  const named = (table: DbTableStat) => (
    <>
      <KindGlyph kind={rowObjectKind(table.kind)} />
      <RowLink mono onClick={() => openTable(table)}>
        <span className="sr-only">Open </span>
        <ObjectName schema={schemas.length > 1 ? table.schema : undefined} name={table.table} />
      </RowLink>
    </>
  )

  return (
    <Panel plain aria-label={title} ref={frame} className="focus-ring" {...RETURNS_FOCUS}>
      {maintenance.dialogs}
      <PanelHeader
        title={title}
        actions={
          <>
            {stats.data?.truncated && (
              <TitleFact>
                the <Fig>{STORAGE_LIMIT}</Fig> largest of more
              </TitleFact>
            )}
            {columns?.has("bloat") && (
              <InfoTip label="What unused space is">
                Unused space is what a table holds and is not using. Where the engine has to work it
                out from its statistics it is an estimate, as fresh as the last analyse.
              </InfoTip>
            )}
            <Stale poll={stats} />
            <SearchInput
              dense
              aria-label={`Filter the ${objects}`}
              placeholder="Filter by name"
              value={filter}
              containerClassName="sm:w-52"
              onChange={(event) => setFilter(event.target.value)}
            />
            {database.length > 0 && (
              <VerbMenu
                verbs={database}
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
      <ViewRead
        poll={stats}
        what={`the ${objects}`}
        locking={engine.can("locks")}
        skeleton={<LoadingPanel plain rows={8} />}
      >
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
              <MaintenanceUnread maintenance={maintenance} />
              <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-6 gap-y-2">
                <ScopeChips
                  schemas={schemas}
                  scope={scope}
                  onScope={(schema) => select({ scope: schema || null })}
                  noun={engine.nouns.containers}
                />
                {whole > 0 && (
                  // One strip for the whole database, the legend its figures:
                  // it does not grow from row to row.
                  <p
                    data-slot="tables-split"
                    className="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1 text-hint text-muted-foreground"
                  >
                    <span className="w-28 shrink-0">
                      <SplitBar
                        label={split.map((part) => `${part.label} ${bytes(part.bytes)}`).join(", ")}
                        parts={split.map((part) => ({
                          key: part.key,
                          count: part.bytes,
                          color: part.color,
                        }))}
                        className="h-1.5"
                      />
                    </span>
                    {split
                      .filter((part) => part.key !== "toastBytes" || part.bytes > 0)
                      .map((part) => (
                        <span key={part.key} className="flex items-center gap-1.5">
                          <span
                            aria-hidden
                            className="size-1.5 rounded-full"
                            style={{ background: part.color }}
                          />
                          {part.label}
                          <span className="numeric text-foreground">{bytes(part.bytes)}</span>
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
                ) : columns ? (
                  // Laid out fixed: every column has its width and the name
                  // has the rest, so the table is exactly as wide as the view.
                  <Table className="table-fixed">
                    <colgroup>
                      <col />
                      <col style={columnWidth("rows")} />
                      <col style={columnWidth("size")} />
                      <col style={columnWidth("share")} />
                      {columns.has("dead") && <col style={columnWidth("dead")} />}
                      {columns.has("bloat") && <col style={columnWidth("bloat")} />}
                      {columns.has("scans") && <col style={columnWidth("scans")} />}
                      {columns.has("parts") && <col style={columnWidth("parts")} />}
                      {columns.has("compression") && <col style={columnWidth("compression")} />}
                      {columns.has("vacuum") && <col style={columnWidth("vacuum")} />}
                      {columns.has("analyze") && <col style={columnWidth("analyze")} />}
                      <col style={columnWidth("actions")} />
                    </colgroup>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        {head(noun, "name")}
                        {head("Rows", "rows", "px-2")}
                        {head("Size", "size", "px-2")}
                        <ShareHead />
                        {columns.has("dead") && head("Dead rows", "dead", "px-2")}
                        {columns.has("bloat") && head("Unused space", "bloat", "px-2")}
                        {columns.has("scans") && head("Sequential scans", "scans", "px-2")}
                        {columns.has("parts") && (
                          <TableHead className="px-2 text-right">Parts</TableHead>
                        )}
                        {columns.has("compression") && (
                          <TableHead className="px-2 text-right">Compressed</TableHead>
                        )}
                        {columns.has("vacuum") && <TableHead className="px-2">Vacuumed</TableHead>}
                        {columns.has("analyze") && <TableHead className="px-2">Analysed</TableHead>}
                        <TableHead className="px-2">
                          <span className="sr-only">Actions</span>
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((table) => (
                        <TableRow
                          key={objectParam(table.schema, table.table)}
                          data-slot="table-row"
                        >
                          <TableCell className="py-2">
                            <span className="flex min-w-0 items-center gap-2">
                              {named(table)}
                              {table.engine && <Tag mono>{table.engine}</Tag>}
                              {table.kind && table.kind !== "table" && (
                                <Tag className="truncate">{table.kind}</Tag>
                              )}
                              {/* What its own column would have said, where the column gave way. */}
                              {!columns.has("analyze") && neverAnalysed(table, every) && (
                                <Tag tone="warning" className="shrink-0">
                                  never analysed
                                </Tag>
                              )}
                            </span>
                          </TableCell>
                          <TableCell className="numeric px-2 py-2 text-right">
                            {table.rows < 0 ? <NoFigure /> : compact(table.rows)}
                          </TableCell>
                          <TableCell className="numeric px-2 py-2 text-right">
                            {bytes(table.totalBytes)}
                          </TableCell>
                          <ShareCell part={table.totalBytes} whole={whole} />
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
                          {columns.has("scans") && (
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
                            <TableCell className="truncate px-2 py-2">
                              <Kept run={lastRun(table.lastVacuum, table.lastAutovacuum)} />
                            </TableCell>
                          )}
                          {columns.has("analyze") && (
                            <TableCell className="truncate px-2 py-2">
                              {neverAnalysed(table, every) ? (
                                <span className="text-warning">never</span>
                              ) : (
                                <Kept run={lastRun(table.lastAnalyze, table.lastAutoanalyze)} />
                              )}
                            </TableCell>
                          )}
                          <TableCell className="px-2 py-1 text-right">
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
                        data-slot="table-row"
                        className="space-y-1 px-4 py-2.5"
                      >
                        <div className="flex min-w-0 items-center gap-2">
                          <span className="flex min-w-0 flex-1 items-center gap-2">
                            {named(table)}
                          </span>
                          <span className="numeric shrink-0 text-xs">
                            {bytes(table.totalBytes)}
                          </span>
                          <TableVerbs table={table} maintenance={maintenance} />
                        </div>
                        {/* One line, whatever the table has to say: what does
                            not fit is cut, and is whole on the table's page. */}
                        <p className="flex min-w-0 gap-x-3 truncate text-hint whitespace-nowrap text-muted-foreground">
                          <span>
                            {table.rows >= 0 ? `${compact(table.rows)} rows` : "rows not counted"}
                          </span>
                          {needsVacuum(table) && (
                            <span className="text-warning">
                              {compact(table.deadRows)} dead rows
                            </span>
                          )}
                          {neverAnalysed(table, every) && (
                            <span className="text-warning">never analysed</span>
                          )}
                        </p>
                      </li>
                    ))}
                  </ul>
                )}
              </PanelBody>
              <Notes notes={data.notes} />
            </div>
          )
        }
      </ViewRead>
    </Panel>
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
