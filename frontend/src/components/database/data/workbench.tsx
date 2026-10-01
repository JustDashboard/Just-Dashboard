"use client"

import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react"
import Link from "next/link"
import {
  CloudUpload,
  Cross,
  Download,
  Plus,
  RefreshClockwise,
  SidebarLeftClose,
  SidebarLeftOpen,
  SidebarRightClose,
  SidebarRightOpen,
  Terminal,
  Wrench,
} from "@/components/icons"
import { ApiError, post } from "@/lib/api"
import { usePanelSize } from "@/lib/panel-size"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import type { PollState } from "@/hooks/use-poll"
import { Statement } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { ResizeHandle } from "@/components/resize-handle"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  DataGrid,
  buildChanges,
  isNewRowId,
  useGridLayout,
  type ChangeSetController,
  type DataGridHandle,
  type GridFilterRequest,
  type GridForeignKeyTarget,
  type GridRowRef,
  type GridSelection,
  type GridSelectionData,
  type GridSort,
} from "@/components/database/grid"
import { SqlReview } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import { EXPORT_FORMATS, EXPORT_ROWS } from "@/components/database/data/export"
import { FilterBar } from "@/components/database/data/filter-bar"
import {
  encodeFilters,
  filterLabel,
  filterText,
  sameFilter,
} from "@/components/database/data/filters"
import { ChangeBar, Pager } from "@/components/database/data/foot"
import { ImportDialog } from "@/components/database/data/import-dialog"
import { RowInspector } from "@/components/database/data/inspector"
import { KindGlyph, ROW_OBJECT_KINDS, rowObjectKind } from "@/components/database/data/kinds"
import {
  changeSummary,
  drawnRows,
  inspectRow,
  refusal,
  relaxGuards,
  rowIdentity,
  type Refusal,
} from "@/components/database/data/rows"
import { inlineStatement } from "@/components/database/data/statement"
import { TableDefinition, TableStructure } from "@/components/database/data/structure"
import type { ChangesResponse, DbTableDetail, Filter } from "@/components/database/data/types"
import type { ExportRequest } from "@/components/database/data/use-export"
import { useBrowse, useExactCount, useGridColumns } from "@/components/database/data/use-table"
import {
  DEFAULT_PAGE_SIZE,
  PAGE_SIZES,
  gridSort,
  grouped,
  type TableView,
  type ViewState,
} from "@/components/database/data/view"

const INSPECTOR = { min: 280, max: 640, fallback: 360 }
const VIEWS: { key: TableView; label: string }[] = [
  { key: "data", label: "Data" },
  { key: "structure", label: "Structure" },
  { key: "definition", label: "Definition" },
]
const NO_ERRORS: Record<string, string> = {}

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max)

/** What the page above can ask of the open table. */
export interface TableWorkbenchHandle {
  /** Opens the statements the staged set would run. */
  review: () => void
  /** Reads the rows again: they were changed from outside the grid. */
  reload: () => void
}

/**
 * One table, worked on: the strip that names it, switches between its rows,
 * its structure and its definition and carries the commands of whichever is
 * open; the grid; and the inspector beside it.
 *
 * It is keyed by table, so nothing of one table — its page, its selection, its
 * count — can be drawn under another's name. What outlives it is passed in:
 * the staged change set (kept by the page above, so a guard can ask before
 * the table is left) and the view, which is the address.
 */
export function TableWorkbench({
  ref,
  schema,
  table,
  detail,
  view,
  setView,
  changeSet,
  guard,
  railOpen,
  onToggleRail,
  wide,
  onExport,
  exporting,
  onWritten,
}: {
  ref?: React.Ref<TableWorkbenchHandle>
  schema: string
  table: string
  detail: PollState<DbTableDetail>
  view: ViewState
  /** Writes the address. */
  setView: (patch: Partial<ViewState>) => void
  changeSet: ChangeSetController
  /** Runs `run` now, or — with edits staged — once the reader has agreed to let them go. */
  guard: (heading: string, run: () => void) => void
  railOpen: boolean
  onToggleRail: () => void
  /** There is room for the rail and the inspector beside the grid. */
  wide: boolean
  onExport: (request: ExportRequest) => void
  exporting: boolean
  /** Rows were written: figures elsewhere on the page are stale. */
  onWritten: () => void
}) {
  const { id, conn, engine, readOnly, href, goto } = useDatabase()
  const { drivers } = useDatabases()
  const { can } = useAuth()
  const info = detail.data
  const kind = rowObjectKind(info?.type)

  const [storedSize, setPageSize] = useViewState(`databases.${id}.data.pageSize`, DEFAULT_PAGE_SIZE)
  const pageSize = (PAGE_SIZES as readonly number[]).includes(storedSize)
    ? storedSize
    : DEFAULT_PAGE_SIZE
  const browse = useBrowse(id, schema, table, view, pageSize)
  const count = useExactCount(id, schema, table, view)
  const page = browse.page
  const columns = useGridColumns(page, info)
  const [layout, setLayout] = useGridLayout(`databases.${id}.data.layout.${schema}.${table}`)
  const grid = useRef<DataGridHandle>(null)

  /* ----------------------------------------------------------- who may edit */

  // A row is told apart by its key only where the engine's key is one: a
  // table with none, and ClickHouse's sorting key, count positions instead.
  const keyed = Boolean(
    info && info.primaryKey.length > 0 && engine.capabilities.rowIdentity !== "none",
  )
  const rowId = useMemo(() => rowIdentity(columns, keyed), [columns, keyed])
  const word = ROW_OBJECT_KINDS[kind].label.toLowerCase()
  // One line each: the grid prints the reason on a single line above the rows.
  const reason = !info
    ? undefined
    : kind !== "table"
      ? `A ${word} is read through its query. Change its rows in the tables it reads.`
      : !engine.can("changeSets")
        ? `${engine.label} cannot change one row in place. Write with a statement in Query.`
        : readOnly
          ? "This connection is protected: the dashboard refuses every change to it."
          : !can("service.control")
            ? "Your role reads this table and may not change it."
            : !keyed && !engine.can("keylessEdits")
              ? `No primary key, and ${engine.label} cannot find a row by its values.`
              : undefined
  const editable = info !== undefined && reason === undefined
  const canDelete = editable && can("destructive")
  const canImport = kind === "table" && engine.can("import") && can("service.control") && !readOnly

  /* -------------------------------------------------------------- the view */

  const operators = useMemo(
    () => drivers?.find((driver) => driver.id === engine.driver)?.filterOps ?? [],
    [drivers, engine.driver],
  )
  const kinds = useMemo(
    () => Object.fromEntries(columns.map((column) => [column.name, column.kind])),
    [columns],
  )
  const sort = useMemo<GridSort>(() => gridSort(view.sort), [view.sort])
  const filtered = view.filters.length > 0

  const changeRows = useCallback(
    (heading: string, patch: Partial<ViewState>) => guard(heading, () => setView(patch)),
    [guard, setView],
  )
  const setFilters = (filters: Filter[]) => changeRows("Filtering the rows", { filters, page: 1 })

  const onFilter = useCallback(
    ({ column, op, value }: GridFilterRequest) => {
      let filter: Filter
      if (op === "is_null" || op === "not_null") filter = { column: column.name, op }
      else {
        const text = filterText(value ?? null)
        if (text === null) {
          notify.warning("This value cannot be filtered on", {
            description:
              "Only the start of it was loaded, or it is a structure no comparison takes.",
          })
          return
        }
        filter = { column: column.name, op, value: text }
      }
      if (view.filters.some((existing) => sameFilter(existing, filter))) return
      changeRows("Filtering the rows", { filters: [...view.filters, filter], page: 1 })
    },
    [view.filters, changeRows],
  )

  // Following a key is a step of history: the referenced table opens on the
  // row, and Back returns to this one as it was.
  const onFollow = useCallback(
    ({ foreignKey, value }: GridForeignKeyTarget) => {
      const text = filterText(value)
      if (text === null) return
      goto("data", {
        schema: foreignKey.schema ?? (schema || null),
        table: foreignKey.table,
        filters: encodeFilters([{ column: foreignKey.column, op: "eq", value: text }]),
      })
    },
    [goto, schema],
  )

  /* -------------------------------------------------------- the inspector */

  const [inspecting, setInspecting] = useViewState("databases.data.inspector", false)
  const [inspectorWidth, setInspectorWidth, resetInspectorWidth] = usePanelSize(
    "databases.data.inspector",
    INSPECTOR.fallback,
  )
  const [activeRow, setActiveRow] = useState(-1)
  const onSelectionChange = useCallback(
    (selection: GridSelection) => setActiveRow(selection.active ? selection.active.row : -1),
    [],
  )
  const onOpenRow = useCallback(
    (row: GridRowRef) => {
      setActiveRow(row.index)
      setInspecting(true)
    },
    [setInspecting],
  )
  const source = useMemo(
    () => ({
      columns,
      rows: page?.rows ?? [],
      clipped: page?.clipped,
      changes: changeSet.changes,
      rowId,
    }),
    [columns, page, changeSet.changes, rowId],
  )
  const inspected = useMemo(
    () => (inspecting && view.view === "data" ? inspectRow(source, activeRow) : null),
    [inspecting, view.view, source, activeRow],
  )

  /* ------------------------------------------------------- the staged set */

  const built = useMemo(
    () => buildChanges(changeSet.changes, { schema, table, columns, guard: true }),
    [changeSet.changes, schema, table, columns],
  )
  const payload = useMemo(
    () => (built.payload ? relaxGuards(built.payload, columns) : null),
    [built.payload, columns],
  )
  const [review, setReview] = useState<string[] | null>(null)
  const [reviewing, setReviewing] = useState(false)
  const [applying, setApplying] = useState(false)
  const [refused, setRefused] = useState<Refusal | null>(null)
  const [errors, setErrors] = useState<Record<string, string>>(NO_ERRORS)
  const dirty = changeSet.dirty
  // A refusal is about the set that was sent; once that set is gone, so is it.
  const shownRefusal = dirty ? refused : null
  const rowErrors = dirty ? errors : NO_ERRORS

  const drawnIndex = useCallback(
    (rowIdWanted: string) => {
      const rows = page?.rows ?? []
      if (isNewRowId(rowIdWanted)) {
        const at = changeSet.changes.inserts.findIndex((row) => row.id === rowIdWanted)
        return at < 0 ? -1 : rows.length + at
      }
      return rows.findIndex((row, index) => (rowId ? rowId(row) : String(index)) === rowIdWanted)
    },
    [page, changeSet.changes.inserts, rowId],
  )

  const turnedDown = useCallback(
    (err: unknown) => {
      const said = refusal(err, built.refs)
      setRefused(said)
      setErrors(said.rowId ? { [said.rowId]: said.message } : NO_ERRORS)
      if (said.rowId) {
        const at = drawnIndex(said.rowId)
        if (at >= 0) grid.current?.revealRow(at)
      }
      if (err instanceof ApiError && err.code === "connection_read_only") {
        notify.error("This connection is protected", err)
      }
    },
    [built.refs, drawnIndex],
  )

  const openReview = useCallback(async () => {
    if (!payload || reviewing) return
    setReviewing(true)
    try {
      const planned = await post<ChangesResponse>(`/databases/${id}/changes`, {
        ...payload,
        dryRun: true,
      })
      setRefused(null)
      setErrors(NO_ERRORS)
      setReview(planned.statements)
    } catch (err) {
      turnedDown(err)
    } finally {
      setReviewing(false)
    }
  }, [id, payload, reviewing, turnedDown])

  // On a database its operator marked production, Apply in the bar reads the
  // statements back first: the one press that writes is made looking at them.
  const cautious = (conn.environment ?? "").trim().toLowerCase() === "production"

  const total = changeSet.counts.total
  const apply = useCallback(async () => {
    if (!payload || applying) return
    setApplying(true)
    try {
      const done = await post<ChangesResponse>(`/databases/${id}/changes`, payload)
      changeSet.reset()
      setRefused(null)
      setErrors(NO_ERRORS)
      setReview(null)
      count.forget()
      browse.refresh()
      onWritten()
      notify.success(
        `Applied ${total === 1 ? "1 change" : `${grouped(total)} changes`} to ${table}`,
        done.attempts > 1
          ? { description: `The engine asked for a retry; it took ${done.attempts} attempts.` }
          : undefined,
      )
    } catch (err) {
      setReview(null)
      turnedDown(err)
    } finally {
      setApplying(false)
    }
  }, [id, payload, applying, changeSet, count, browse, onWritten, total, table, turnedDown])

  // Asked for from outside: the guard's "Review them".
  const reload = browse.refresh
  const forget = count.forget
  useImperativeHandle(
    ref,
    () => ({
      review: () => void openReview(),
      reload: () => {
        forget()
        reload()
      },
    }),
    [openReview, forget, reload],
  )

  // Ctrl/Cmd+S reads the set back before anything is written: the reader's
  // habit of saving opens the review, wherever the keyboard is.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() !== "s" || !(event.metaKey || event.ctrlKey) || event.altKey)
        return
      event.preventDefault()
      if (dirty && view.view === "data") void openReview()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [dirty, view.view, openReview])

  /* ---------------------------------------------------------- copy as SQL */

  const [copied, setCopied] = useState<string | null>(null)
  const onCopySQL = useCallback(
    async (data: GridSelectionData) => {
      // A preview is not the value: an INSERT built from one would hold the
      // first bytes as if they were all of it.
      if (data.previews.length > 0) {
        notify.warning("Some of these values are only partly loaded", {
          description: "Open the row and load them whole, then copy again.",
        })
        return
      }
      // A staged new row's unset columns would read NULL here and take their
      // defaults when applied; such a row is copied once it exists.
      const rows = data.rows
        .filter((_, index) => !isNewRowId(data.rowIds[index]))
        .map((row) => Object.fromEntries(data.columns.map((column, i) => [column.name, row[i]])))
      if (rows.length === 0) {
        notify.info("Apply the new rows first", {
          description: "A row that is only staged has no stored values to write a statement from.",
        })
        return
      }
      const text = post<{ sql: string }>(`/databases/${id}/rows/sql`, { schema, table, rows }).then(
        (answer) => answer.sql,
      )
      try {
        // The clipboard is promised the text inside the gesture; a write made
        // after the request came back is one Safari refuses.
        await navigator.clipboard.write([
          new ClipboardItem({
            "text/plain": text.then((sql) => new Blob([sql], { type: "text/plain" })),
          }),
        ])
        notify.success(rows.length === 1 ? "Copied 1 INSERT" : `Copied ${rows.length} INSERTs`)
      } catch {
        try {
          // The statements, shown, with a Copy the reader presses themselves.
          setCopied(await text)
        } catch (err) {
          notify.error("Could not write the statements", err)
        }
      }
    },
    [id, schema, table],
  )

  /* ------------------------------------------------------------- commands */

  const [importing, setImporting] = useState(false)
  const [onlyShown, setOnlyShown] = useState(false)
  const hidden = layout.hidden.length > 0

  const exportAs = (format: ExportRequest["format"]) => {
    const visible = new Set(layout.hidden)
    onExport({
      schema,
      table,
      format,
      view: { filters: view.filters, match: view.match, sort: view.sort },
      columns:
        onlyShown && hidden
          ? columns.filter((column) => !visible.has(column.key)).map((column) => column.name)
          : undefined,
    })
  }

  const openAsQuery = () => {
    if (!page) return
    const written = inlineStatement(
      page.statement,
      view.filters,
      { limit: page.limit, offset: page.offset },
      kinds,
    )
    goto("query", {
      sql:
        written ??
        `-- The values of this view could not be written in. Replace each marker before running.\n${page.statement}`,
    })
  }

  const refresh = () =>
    guard("Reading the rows again", () => {
      count.forget()
      detail.refresh()
      browse.refresh()
    })

  /* --------------------------------------------------------------- render */

  const structureHref = href("schema", { schema: schema || null, table })
  const canAlter = can("service.control") && !readOnly && engine.can("ddl") && engine.has("schema")
  const missing = detail.error instanceof ApiError && detail.error.status === 404

  const strip = (
    // The strip lays itself out by its own width, not the window's: the rail
    // and the row panel beside it decide how much of the window it has. Its
    // commands keep their glyphs and drop their words as it narrows, and the
    // name gives way before anything wraps.
    <div className="@container shrink-0 border-b border-hairline bg-surface-header">
      <div className="flex min-h-10 items-stretch pr-1.5 pl-1.5 @max-lg:flex-wrap">
        <div className="flex max-w-full min-w-0 items-center gap-1.5 py-1 pr-2 @max-lg:max-w-[11rem]">
          <IconAction
            label={railOpen ? "Hide the tables" : "Show the tables"}
            aria-pressed={railOpen}
            className="size-7 shrink-0"
            onClick={onToggleRail}
          >
            {railOpen ? <SidebarLeftClose /> : <SidebarLeftOpen />}
          </IconAction>
          <KindGlyph kind={kind} />
          <h2 className="flex min-w-0 items-baseline font-mono text-body font-medium">
            {schema && (
              <span className="shrink-0 text-muted-foreground @max-lg:hidden">{schema}.</span>
            )}
            <span className="truncate">{table}</span>
          </h2>
          {kind !== "table" && <Tag>{word}</Tag>}
        </div>
        {!missing && (
          <div
            role="tablist"
            aria-label="View of the table"
            className="flex shrink-0 items-stretch"
          >
            {VIEWS.map((entry) => (
              <button
                key={entry.key}
                type="button"
                role="tab"
                aria-selected={view.view === entry.key}
                className={cn(tabClasses(view.view === entry.key, "h-10"), "max-sm:px-2")}
                onClick={() => setView({ view: entry.key })}
              >
                {entry.label}
              </button>
            ))}
          </div>
        )}
        {!missing && (
          <div className="ml-auto flex shrink-0 items-center justify-end gap-1 py-1 pl-2">
            {view.view === "data" ? (
              <>
                {editable && (
                  <Button
                    type="button"
                    size="xs"
                    variant="outline"
                    disabled={!page}
                    onClick={() => grid.current?.insertRow()}
                  >
                    <Plus />
                    Insert row
                  </Button>
                )}
                {canImport && info && (
                  <Button
                    type="button"
                    size="xs"
                    variant="outline"
                    aria-label="Import"
                    onClick={() => guard("Importing", () => setImporting(true))}
                  >
                    <CloudUpload />
                    <span className="hidden @3xl:inline">Import</span>
                  </Button>
                )}
                {engine.capabilities.exportFormats.length > 0 && (
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        type="button"
                        size="xs"
                        variant="outline"
                        aria-label="Export"
                        pending={exporting}
                      >
                        <Download />
                        <span className="hidden @3xl:inline">Export</span>
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="w-64">
                      {/* A fact about what the menu's words will save, in the
                      quiet voice: a sentence is not a group's name. */}
                      <p className="px-2 pt-1.5 pb-1 text-hint text-muted-foreground">
                        {filtered ? "The rows these filters match" : "Every row of the table"}, up
                        to {grouped(EXPORT_ROWS)}
                      </p>
                      {engine.capabilities.exportFormats.map((format) => (
                        <DropdownMenuItem key={format} onSelect={() => exportAs(format)}>
                          <span className="w-20 shrink-0">{EXPORT_FORMATS[format].label}</span>
                          <span className="min-w-0 truncate text-hint text-muted-foreground">
                            {EXPORT_FORMATS[format].detail}
                          </span>
                        </DropdownMenuItem>
                      ))}
                      {hidden && engine.can("exportColumns") && (
                        <>
                          <DropdownMenuSeparator />
                          <DropdownMenuCheckboxItem
                            checked={onlyShown}
                            onSelect={(event) => event.preventDefault()}
                            onCheckedChange={(checked) => setOnlyShown(checked === true)}
                          >
                            Only the columns shown
                          </DropdownMenuCheckboxItem>
                        </>
                      )}
                    </DropdownMenuContent>
                  </DropdownMenu>
                )}
                {engine.has("query") && can("service.control") && (
                  <Button
                    type="button"
                    size="xs"
                    variant="ghost"
                    aria-label="Open as query"
                    disabled={!page}
                    onClick={openAsQuery}
                  >
                    <Terminal />
                    <span className="hidden @4xl:inline">Open as query</span>
                  </Button>
                )}
                <IconAction label="Read the rows again" className="size-7" onClick={refresh}>
                  <RefreshClockwise />
                </IconAction>
                <IconAction
                  label={inspecting ? "Hide the row" : "Show the row"}
                  aria-pressed={inspecting}
                  className="size-7"
                  onClick={() => setInspecting(!inspecting)}
                >
                  {inspecting ? <SidebarRightClose /> : <SidebarRightOpen />}
                </IconAction>
              </>
            ) : (
              engine.has("schema") && (
                <Button type="button" size="xs" variant="outline" asChild>
                  <Link href={structureHref}>
                    <Wrench />
                    {canAlter ? "Change it in Schema" : "Open in Schema"}
                  </Link>
                </Button>
              )
            )}
          </div>
        )}
      </div>
    </div>
  )

  let body: React.ReactNode
  if (missing) {
    body = (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyState
          className="border-0"
          title={`No ${engine.nouns.object} called ${table}`}
          description={`${schema ? `${schema} holds` : `This ${engine.nouns.container} holds`} nothing by that name. It may have been renamed or dropped since the link was made.`}
          action={
            <Button size="sm" variant="outline" asChild>
              <Link href={href("data", { schema: schema || null, table: null })}>
                See what is there
              </Link>
            </Button>
          }
        />
      </div>
    )
  } else if (view.view !== "data") {
    body = !info ? (
      detail.error ? (
        <div className="p-4">
          <ErrorState error={detail.error} onRetry={detail.refresh} />
        </div>
      ) : (
        <LoadingRows rows={8} className="p-4" />
      )
    ) : view.view === "structure" ? (
      <TableStructure detail={info} />
    ) : (
      <TableDefinition detail={info} />
    )
  } else if (!info && !detail.error) {
    // What the table is made of has not come yet: the grid has no column to
    // draw a header from, and would stand as an empty frame with a gutter.
    body = <GridSilhouette />
  } else {
    const viewError = browse.error && (filtered || view.sort.length > 0)
    body = (
      <DataGrid
        ref={grid}
        label={`${table} rows`}
        columns={columns}
        rows={page?.rows ?? []}
        rowId={rowId}
        clipped={page?.clipped}
        rowNumberOffset={page?.offset ?? 0}
        sort={sort}
        onSortChange={(next) => changeRows("Sorting the rows", { sort: [...next], page: 1 })}
        layout={layout}
        onLayoutChange={setLayout}
        onSelectionChange={onSelectionChange}
        changeSet={changeSet}
        editable={editable}
        canDelete={canDelete}
        defaultOnUpdate={engine.can("updateDefault")}
        readOnlyReason={reason}
        rowErrors={rowErrors}
        loading={browse.loading || detail.loading}
        error={browse.error ?? detail.error ?? null}
        onRetry={() => {
          detail.refresh()
          browse.refresh()
        }}
        onOpenRow={onOpenRow}
        onFilter={operators.length > 0 ? onFilter : undefined}
        onFollowForeignKey={onFollow}
        onCopySQL={(data) => void onCopySQL(data)}
        toolbar={
          // One line, whatever it holds: a run of chips that wrapped would
          // move the grid down a row each time a filter was added.
          <div className="flex min-w-0 flex-1 [scrollbar-width:none] items-center gap-1.5 overflow-x-auto [&::-webkit-scrollbar]:hidden">
            <FilterBar
              columns={columns}
              filters={view.filters}
              match={view.match}
              sort={view.sort}
              operators={operators}
              onFiltersChange={setFilters}
              onMatchChange={(match) => changeRows("Filtering the rows", { match, page: 1 })}
              onSortChange={(next) => changeRows("Sorting the rows", { sort: next, page: 1 })}
            />
          </div>
        }
        banner={
          <>
            {editable && !keyed && (
              <BannerLine>
                <Tag>No key</Tag>
                <span className="min-w-0 truncate">
                  A row is found by all of its values. A change that would touch more than one row
                  is refused.
                </span>
              </BannerLine>
            )}
            {viewError && (
              <BannerLine>
                <span className="min-w-0 truncate">
                  {view.filters
                    .map((filter) => filterLabel(filter, kinds[filter.column]))
                    .join(" · ") || "This sort"}{" "}
                  could not be applied.
                </span>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  className="ml-auto shrink-0"
                  onClick={() =>
                    changeRows("Clearing the sort and the filters", {
                      filters: [],
                      sort: [],
                      match: "all",
                      page: 1,
                    })
                  }
                >
                  Clear sort and filters
                </Button>
              </BannerLine>
            )}
            {shownRefusal && (
              <div
                role="alert"
                className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-rule-danger bg-wash-danger px-2.5 py-1.5 text-hint"
              >
                <span className="font-medium">Nothing was written.</span>
                <span className="min-w-0 flex-1">
                  {shownRefusal.position !== undefined &&
                    `Change ${shownRefusal.position} of ${shownRefusal.total}: `}
                  {shownRefusal.message}
                </span>
                {shownRefusal.rowId && (
                  <Button
                    type="button"
                    size="xs"
                    variant="outline"
                    onClick={() => {
                      changeSet.revertRow(shownRefusal.rowId!)
                      setRefused(null)
                      setErrors(NO_ERRORS)
                      // What the row holds now is what refused the change;
                      // a keyed page can be read again under the rest of the set.
                      if (keyed) browse.refresh()
                    }}
                  >
                    Take that change out
                  </Button>
                )}
                <IconAction
                  label="Dismiss"
                  className="size-6"
                  onClick={() => {
                    setRefused(null)
                    setErrors(NO_ERRORS)
                  }}
                >
                  <Cross />
                </IconAction>
              </div>
            )}
          </>
        }
        empty={
          page && page.offset > 0 ? (
            <GridEmpty
              title="No rows on this page"
              description="The table ends before here."
              action="Back to the first page"
              onAction={() => changeRows("Turning the page", { page: 1 })}
            />
          ) : filtered ? (
            <GridEmpty
              title="No row matches"
              description={`Nothing in ${table} meets ${view.filters.length === 1 ? "this condition" : view.match === "any" ? "any of these conditions" : "all of these conditions"}.`}
              action="Clear the filters"
              onAction={() =>
                changeRows("Clearing the filters", { filters: [], match: "all", page: 1 })
              }
            />
          ) : (
            <GridEmpty
              title={`${table} has no rows`}
              description={
                editable
                  ? "Insert one here, or import a file."
                  : "Nothing has been written to it yet."
              }
              action={editable ? "Insert row" : undefined}
              onAction={() => grid.current?.insertRow()}
            />
          )
        }
        footer={
          dirty ? (
            <ChangeBar
              counts={changeSet.counts}
              problems={built.problems}
              applying={applying}
              reviewing={reviewing}
              confirms={cautious}
              onReview={() => void openReview()}
              onDiscard={() => {
                changeSet.discardAll()
                setRefused(null)
                setErrors(NO_ERRORS)
              }}
              onApply={() => void (cautious ? openReview() : apply())}
            />
          ) : (
            page && (
              <Pager
                page={page}
                filtered={filtered}
                exact={count.exact}
                counting={count.counting}
                countError={count.error}
                onCount={count.count}
                pageNumber={view.page}
                pageSize={pageSize}
                onPage={(next) => setView({ page: next })}
                onPageSize={(size) => {
                  setPageSize(size)
                  setView({ page: 1 })
                }}
              />
            )
          )
        }
      />
    )
  }

  const inspectorShown = inspecting && view.view === "data" && !missing && info !== undefined

  return (
    <>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {strip}
        {body}
      </div>

      {inspectorShown && (
        <div
          style={{ "--jd-data-inspector": `${inspectorWidth}px` } as React.CSSProperties}
          className={cn(
            "flex min-h-0 flex-col border-hairline bg-card",
            wide ? "relative w-(--jd-data-inspector) shrink-0 border-l" : "absolute inset-0 z-30",
          )}
        >
          {wide && (
            <ResizeHandle
              side="right"
              label="Row panel width"
              value={inspectorWidth}
              min={INSPECTOR.min}
              max={INSPECTOR.max}
              onChange={(px, commit) =>
                setInspectorWidth(clamp(px, INSPECTOR.min, INSPECTOR.max), commit)
              }
              onReset={resetInspectorWidth}
              className="absolute inset-y-0 -left-1 z-20"
            />
          )}
          <RowInspector
            schema={schema}
            table={table}
            detail={info}
            columns={columns}
            row={inspected}
            rowNumber={
              inspected && inspected.state !== "inserted" && page
                ? page.offset + inspected.index + 1
                : null
            }
            rows={drawnRows(source)}
            editable={editable}
            canInsert={editable}
            canDelete={canDelete}
            defaultOnUpdate={engine.can("updateDefault")}
            changeSet={changeSet}
            onMove={(index) => grid.current?.revealRow(index)}
            onClose={() => setInspecting(false)}
          />
        </div>
      )}

      {review && (
        <SqlReview
          open
          onOpenChange={(open) => !open && !applying && setReview(null)}
          title={`Apply ${total === 1 ? "1 change" : `${grouped(total)} changes`} to ${table}`}
          statements={review}
          command="Apply"
          pending={applying}
          danger={changeSet.counts.deletes > 0}
          note={
            cautious
              ? "A production database. One transaction: every statement takes effect, or none does."
              : "One transaction: every statement takes effect, or none does."
          }
          onRun={() => void apply()}
        >
          <p className="numeric text-body text-muted-foreground">
            {changeSummary(changeSet.counts)}
          </p>
        </SqlReview>
      )}
      {info && (
        <ImportDialog
          open={importing}
          onOpenChange={setImporting}
          detail={info}
          onImported={() => {
            count.forget()
            browse.refresh()
            onWritten()
          }}
        />
      )}
      <Modal
        open={copied !== null}
        onOpenChange={(open) => !open && setCopied(null)}
        size="lg"
        title="The rows as INSERT statements"
        description="The browser did not let the page write to the clipboard; copy the text here"
        initialFocus="body"
        footer={<Button onClick={() => setCopied(null)}>Done</Button>}
      >
        <Statement label="Statements" sql={copied ?? ""} placeholder="" />
      </Modal>
    </>
  )
}

const SILHOUETTE = ["w-10", "w-40", "w-28", "w-16", "w-32", "w-20", "w-36", "w-24"]

/** The shape of the grid to come: its strip, a header and rows of values, before any column is known. */
function GridSilhouette() {
  return (
    <div role="status" aria-label="Loading the rows" className="min-h-0 flex-1 overflow-hidden">
      <div className="flex h-9 items-center border-b border-hairline bg-surface-header px-2.5">
        <Skeleton className="h-5 w-16" />
      </div>
      {Array.from({ length: 32 }, (_, row) => (
        <div
          key={row}
          className="flex h-[30px] items-center gap-8 border-b border-hairline px-4"
          aria-hidden
        >
          {SILHOUETTE.map((width, column) => (
            <Skeleton
              key={column}
              className={cn("shrink-0", row === 0 ? "h-2.5 opacity-70" : "h-3", width)}
            />
          ))}
        </div>
      ))}
    </div>
  )
}

/** One quiet line between the grid's strip and its rows. */
function BannerLine({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex shrink-0 items-center gap-2 border-b border-hairline px-2.5 py-1 text-hint text-muted-foreground">
      {children}
    </div>
  )
}

/** What stands where the rows would be when there are none, and the way on. */
function GridEmpty({
  title,
  description,
  action,
  onAction,
}: {
  title: string
  description: string
  action?: string
  onAction?: () => void
}) {
  return (
    <EmptyState
      className="border-0 py-10"
      title={title}
      description={description}
      action={
        action && (
          <Button type="button" size="sm" variant="outline" onClick={onAction}>
            {action}
          </Button>
        )
      }
    />
  )
}
