"use client"

import { useMemo } from "react"
import Link from "next/link"
import { useParams } from "next/navigation"
import { CodeBracket, GridSquare } from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SplitBar, percent as share } from "@/components/rank-list"
import { EmptyNote, EmptyState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { KindGlyph, rowObjectKind } from "@/components/database/data/kinds"
import { CouldNotRead } from "@/components/database/home/blocks"
import { compact } from "@/components/database/home/readings"
import { EngineMark } from "@/components/database/kit"
import { parseTableSegment, tableHref } from "@/components/database/ops/performance-address"
import { STORAGE_LIMIT, readTableStats } from "@/components/database/ops/performance-api"
import {
  DetailFrame,
  DetailGate,
  DetailHead,
  DetailRail,
  DetailTabs,
  stepOf,
  useDetailTab,
  type DetailView,
  type RailRow,
} from "@/components/database/ops/performance-detail"
import {
  TABLE_PARTS,
  tableStatsKey,
  tablesFilterKey,
  tablesOrderKey,
} from "@/components/database/ops/performance-lists"
import {
  MaintenanceList,
  MaintenanceUnread,
  useMaintenance,
} from "@/components/database/ops/performance-maintenance"
import { NoFigure, ObjectName } from "@/components/database/ops/performance-parts"
import {
  compressionRatio,
  counted,
  deadShare,
  isTableSort,
  lastRun,
  needsVacuum,
  neverAnalysed,
  qualified,
  seqShare,
  sortTables,
  tableColumns,
  type TableColumn,
  type TableSort,
} from "@/components/database/ops/performance-storage"
import { Fig } from "@/components/database/ops/performance-words"
import type { DbTableStat } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

type Tab = "figures" | "maintenance"

/**
 * One table, as a place of its own: `/databases/<id>/performance/tables/<schema>.<name>`.
 *
 * It was a sheet over the Tables view: every figure the engine keeps about
 * it, the ways into its rows and its definition, and its maintenance — each
 * action with the engine's own sentence about what it does and what it
 * locks. The same, with room, and the list beside it in the order it was
 * left in.
 */
export function TablePage() {
  const { engine } = useDatabase()
  const params = useParams<{ name: string }>()
  const named = parseTableSegment(params.name ?? "")
  return (
    <DetailGate has={engine.kind === "sql" && engine.can("tableStats")} thing="a table's figures">
      <Table key={params.name} named={named} />
    </DetailGate>
  )
}

function Table({ named }: { named: { schema: string; name: string } | null }) {
  const { id, conn, engine, href, goto, param } = useDatabase()
  const stats = usePoll((signal) => readTableStats(id, signal), 60_000, [id], {
    key: tableStatsKey(id),
  })
  const maintenance = useMaintenance(stats.refresh)
  const [filter, setFilter] = useSessionState(tablesFilterKey(id), "")
  const [order] = useViewState<{ by: string; descending: boolean }>(tablesOrderKey(id), {
    by: "size",
    descending: true,
  })
  const by: TableSort = isTableSort(order.by) ? order.by : "size"
  const all = stats.data?.tables
  const every = useMemo(() => tableColumns(all ?? []), [all])
  const table = named
    ? all?.find((entry) => entry.schema === named.schema && entry.table === named.name)
    : undefined
  const schemas = useMemo(() => new Set((all ?? []).map((entry) => entry.schema)), [all])
  // The schema the list was narrowed to goes with the reader: the rail is
  // that list. One the database does not hold narrows nothing.
  const asked = param("scope")
  const scope = asked && schemas.has(asked) ? asked : ""
  const whole = useMemo(
    () => (all ?? []).reduce((sum, entry) => sum + Math.max(entry.totalBytes, 0), 0),
    [all],
  )

  const verbs = table ? maintenance.forTarget({ schema: table.schema, table: table.table }) : []
  const housekept = every.has("vacuum") || every.has("analyze")
  const maintained = verbs.length > 0
  const views = useMemo<DetailView<Tab>[]>(
    () => [
      { key: "figures", label: "Figures" },
      ...(housekept || maintained ? [{ key: "maintenance" as const, label: "Maintenance" }] : []),
    ],
    [housekept, maintained],
  )
  const [tab, setTab] = useDetailTab(views)
  const carried = { tab: tab === "figures" ? null : tab, scope: scope || null }

  const rows = useMemo<RailRow[]>(() => {
    const needle = filter.trim().toLowerCase()
    const inScope = (all ?? []).filter(
      (entry) =>
        (!scope || entry.schema === scope) &&
        (!needle || qualified(entry.schema, entry.table).toLowerCase().includes(needle)),
    )
    return sortTables(inScope, by, order.descending).map((entry) => ({
      key: qualified(entry.schema, entry.table),
      href: tableHref(id, entry.schema, entry.table, carried),
      label: qualified(entry.schema, entry.table),
      mark: <TableTile kind={entry.kind} small />,
      title: (
        <ObjectName
          schema={schemas.size > 1 && !scope ? entry.schema : undefined}
          name={entry.table}
        />
      ),
      sub: entry.rows < 0 ? "rows not counted" : `${compact(entry.rows)} rows`,
      figure: bytes(entry.totalBytes),
      figureSub: whole > 0 ? share(entry.totalBytes / whole) : undefined,
    }))
    // `carried` is two strings: held by what they are.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    all,
    scope,
    filter,
    by,
    order.descending,
    id,
    schemas.size,
    whole,
    carried.tab,
    carried.scope,
  ])
  const current = named ? qualified(named.schema, named.name) : null
  const objects = engine.nouns.objects
  const caption = !stats.data
    ? stats.error
      ? `The ${objects} could not be read`
      : `Reading the ${objects}…`
    : `${rows.length === (all?.length ?? 0) ? rows.length : `${rows.length} of ${all?.length}`} ${objects}${scope ? ` in ${scope}` : ""} · by ${by === "name" ? "name" : by === "scans" ? "sequential scans" : by === "dead" ? "dead rows" : by === "bloat" ? "unused space" : by}`

  const back = {
    href: href("performance", { view: "tables", scope: scope || null }),
    label: objects[0].toUpperCase() + objects.slice(1),
  }
  const head = (
    <DetailHead
      title={`${conn.name} · ${current ?? engine.nouns.object}`}
      back={back}
      noun={engine.nouns.object}
      step={stepOf(rows, current)}
    />
  )
  const rail = (
    <DetailRail
      label={back.label}
      placeholder="Filter by name"
      query={filter}
      onQuery={setFilter}
      caption={caption}
      rows={rows}
      current={current}
      loading={!stats.data && !stats.error}
      empty={stats.error && !stats.data ? "" : `No ${engine.nouns.object} matches.`}
    />
  )

  if (!table) {
    return (
      <DetailFrame rail={rail}>
        {head}
        {named && !stats.data && !stats.error ? (
          <>
            <HostIdentity
              className="border-b-0 pb-0"
              logo={<TableTile />}
              title={<ObjectName schema={named.schema || undefined} name={named.name} />}
              state={<TextShimmer className="text-xs">Reading its figures…</TextShimmer>}
              facts={<span>&nbsp;</span>}
            />
            {/* The page's own shape while it is read: the strip is where it will be. */}
            <DetailTabs
              label={`Views of ${qualified(named.schema, named.name)}`}
              views={views}
              tab={tab}
              onTab={setTab}
            >
              <LoadingRows />
            </DetailTabs>
          </>
        ) : stats.error && !stats.data ? (
          <CouldNotRead what={`the ${objects}`} error={stats.error} onRetry={stats.refresh} />
        ) : stats.data && !stats.data.supported ? (
          <EmptyState
            mark={<EngineMark engine={engine} size="md" />}
            title={`The ${engine.nouns.object} statistics cannot be read`}
            description={stats.data.reason}
          />
        ) : (
          <EmptyState
            mark={<EngineMark engine={engine} size="md" />}
            title={
              named
                ? `This database holds no ${engine.nouns.object} named ${qualified(named.schema, named.name)}`
                : `That is not a ${engine.nouns.object}'s address`
            }
            description={
              stats.data?.truncated
                ? `It may have been dropped or renamed, or be smaller than the ${STORAGE_LIMIT} largest this page reads.`
                : "It may have been dropped or renamed since the link was made."
            }
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href={back.href}>Back to {back.label.toLowerCase()}</Link>
              </Button>
            }
          />
        )}
      </DetailFrame>
    )
  }

  const due = needsVacuum(table)
  const unanalysed = neverAnalysed(table, every)
  const vacuumed = lastRun(table.lastVacuum, table.lastAutovacuum)
  const menu: Verb[] = [
    ...(engine.has("schema")
      ? [
          {
            key: "schema",
            label: "Open in Schema",
            icon: CodeBracket,
            run: () => goto("schema", { schema: table.schema, table: table.table }),
          },
        ]
      : []),
    ...maintenance.verbsFor({ schema: table.schema, table: table.table }),
  ]

  return (
    <DetailFrame rail={rail}>
      {maintenance.dialogs}
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        {head}
        <HostIdentity
          label={`About ${qualified(table.schema, table.table)}`}
          className="animate-rise border-b-0 pb-0"
          logo={<TableTile kind={table.kind} />}
          title={<ObjectName schema={table.schema || undefined} name={table.table} />}
          state={
            due ? (
              <Status tone="warning" label="a vacuum is due" />
            ) : unanalysed ? (
              <Status tone="warning" label="never analysed" />
            ) : (
              <span className="text-xs">&nbsp;</span>
            )
          }
          facts={
            <>
              <span>
                <Fig>{bytes(table.totalBytes)}</Fig>
                {whole > 0 && ` · ${share(table.totalBytes / whole)} of the database`}
              </span>
              <FactDot />
              <span className="numeric">
                {table.rows < 0 ? "rows not counted" : `${compact(table.rows)} rows`}
              </span>
              {table.engine && (
                <>
                  <FactDot />
                  <span className="font-mono">{table.engine}</span>
                </>
              )}
              {table.kind && table.kind !== "table" && (
                <>
                  <FactDot />
                  <span>{table.kind}</span>
                </>
              )}
              {every.has("vacuum") && (
                <>
                  <FactDot />
                  <span title={vacuumed ? timestamp(vacuumed.at) : undefined}>
                    {vacuumed ? `vacuumed ${relativeTime(vacuumed.at)}` : "never vacuumed"}
                  </span>
                </>
              )}
            </>
          }
          command={
            <Button size="sm" asChild>
              <Link href={href("data", { schema: table.schema, table: table.table })}>
                <GridSquare />
                <span>
                  <span className="max-sm:sr-only">Open in </span>Data
                </span>
              </Link>
            </Button>
          }
          menu={
            menu.length > 0 && (
              <VerbMenu verbs={menu} label={`More for ${qualified(table.schema, table.table)}`} />
            )
          }
        />
      </div>

      <DetailTabs
        label={`Views of ${qualified(table.schema, table.table)}`}
        views={views}
        tab={tab}
        onTab={setTab}
      >
        {tab === "figures" ? (
          <Figures table={table} columns={every} />
        ) : (
          <div className="space-y-8">
            <MaintenanceUnread maintenance={maintenance} />
            {housekept && <Housekeeping table={table} columns={every} />}
            {verbs.length > 0 && (
              <Panel plain aria-label="Maintenance">
                <PanelHeader title="Run" />
                {/* The list's own top rule is the title's hairline. */}
                <PanelBody flush className="[&>ul]:border-t-0">
                  <MaintenanceList
                    verbs={verbs}
                    maintenance={maintenance}
                    on={qualified(table.schema, table.table)}
                  />
                </PanelBody>
              </Panel>
            )}
            {!housekept && verbs.length === 0 && (
              <EmptyNote>This engine offers no maintenance for it.</EmptyNote>
            )}
          </div>
        )}
      </DetailTabs>
    </DetailFrame>
  )
}

/** A table's mark: what kind of thing it is, in its hue, on the identity line's tile. */
function TableTile({ kind, small }: { kind?: string; small?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex shrink-0 items-center justify-center border border-hairline bg-card",
        small ? "size-7 rounded-md" : "size-10 rounded-lg",
      )}
    >
      <KindGlyph kind={rowObjectKind(kind)} className={small ? "size-3.5" : "size-5"} />
    </span>
  )
}

const figure = (value: number | undefined) =>
  counted(value) === undefined ? <NoFigure /> : (value as number).toLocaleString()

/**
 * What the engine keeps about the table: what its bytes are, as one strip
 * and the figures under it, its rows, and how it is read. A figure the engine
 * does not keep is left out, never drawn as a zero.
 */
function Figures({ table, columns }: { table: DbTableStat; columns: ReadonlySet<TableColumn> }) {
  const parts = TABLE_PARTS.map((part) => ({ ...part, bytes: Math.max(table[part.key], 0) }))
  const ratio = compressionRatio(table)
  const dead = counted(table.deadRows)
  const deadOf = deadShare(table)
  const seq = seqShare(table)
  const rowsKept = columns.has("dead") || counted(table.inserts) !== undefined
  return (
    <div className="grid gap-x-10 gap-y-8 lg:grid-cols-2 [&>*]:min-w-0" data-slot="table-figures">
      <Panel plain aria-label="Size" className="lg:col-span-2">
        <PanelHeader
          title="Size"
          actions={<span className="numeric text-hint">{bytes(table.totalBytes)}</span>}
        />
        <PanelBody className="space-y-3">
          <SplitBar
            label={parts.map((part) => `${part.label} ${bytes(part.bytes)}`).join(", ")}
            parts={parts.map((part) => ({ key: part.key, count: part.bytes, color: part.color }))}
          />
          <p className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-1 text-body">
            {parts
              .filter((part) => part.key !== "toastBytes" || part.bytes > 0)
              .map((part) => (
                <span key={part.key} className="flex items-center gap-2">
                  <span
                    aria-hidden
                    className="size-2 rounded-full"
                    style={{ background: part.color }}
                  />
                  <span className="text-muted-foreground">{part.label}</span>
                  <span className="numeric font-medium">{bytes(part.bytes)}</span>
                </span>
              ))}
          </p>
          {(counted(table.bloatBytes) !== undefined ||
            ratio !== undefined ||
            table.parts !== undefined) && (
            <DetailList>
              {counted(table.bloatBytes) !== undefined && (
                <Detail label="Unused, estimated">{bytes(table.bloatBytes)}</Detail>
              )}
              {ratio !== undefined && (
                <Detail label="Before compression">
                  {bytes(table.uncompressedBytes ?? 0)} · {ratio.toFixed(1)}× smaller on disk
                </Detail>
              )}
              {table.parts !== undefined && (
                <Detail label="Parts">{table.parts.toLocaleString()}</Detail>
              )}
            </DetailList>
          )}
        </PanelBody>
      </Panel>

      {rowsKept && (
        <Panel plain aria-label="Rows">
          <PanelHeader
            title="Rows"
            actions={
              <span className="numeric text-hint">
                {table.rows < 0 ? "not counted" : table.rows.toLocaleString()}
              </span>
            }
          />
          <PanelBody>
            <DetailList>
              {columns.has("dead") && (
                <Detail label="Dead">
                  {dead === undefined ? (
                    <NoFigure />
                  ) : (
                    <span className={cn("numeric", needsVacuum(table) && "text-warning")}>
                      {dead.toLocaleString()}
                      {dead > 0 && deadOf !== undefined && (
                        <span className="text-muted-foreground"> · {share(deadOf)}</span>
                      )}
                    </span>
                  )}
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
          </PanelBody>
        </Panel>
      )}

      {columns.has("scans") && (
        <Panel plain aria-label="How it is read">
          <PanelHeader
            title="How it is read"
            actions={
              seq !== undefined && (
                <span className="numeric text-hint">{share(seq)} sequential</span>
              )
            }
          />
          <PanelBody>
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
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}

/** When the table was last vacuumed and analysed, by hand and by the engine itself. */
function Housekeeping({
  table,
  columns,
}: {
  table: DbTableStat
  columns: ReadonlySet<TableColumn>
}) {
  const when = (at: string | undefined) =>
    at ? (
      <span title={timestamp(at)}>{relativeTime(at)}</span>
    ) : (
      <span className="text-muted-foreground">never</span>
    )
  return (
    <Panel plain aria-label="Housekeeping">
      <PanelHeader title="Housekeeping" />
      <PanelBody>
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
      </PanelBody>
    </Panel>
  )
}
