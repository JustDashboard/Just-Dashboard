"use client"

import { useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Copy, Cross, Filter, Layout, Plus, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, downloadUrl, get, patch, post } from "@/lib/api"
import { plural } from "@/lib/format"
import type {
  DbConnection,
  DbDriverInfo,
  DbFilter,
  DbForeignKey,
  DbRelations,
  DbTable,
  DbTableDetail,
  QueryResult,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Toggle } from "@/components/ui/toggle"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Pane, PaneFooter, PaneHeader } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { ResultGrid } from "@/components/database/result-grid"
import { RowEditor } from "@/components/database/row-editor"
import { ImportDialog } from "@/components/database/import-dialog"
import { TableRail, type TableSelection } from "@/components/database/table-rail"
import { TableMenu } from "@/components/database/table-actions"
import {
  AddColumnDialog,
  CreateIndexDialog,
  CreateTableDialog,
  RenameDialog,
} from "@/components/database/ddl-dialogs"
import { copyText } from "@/lib/clipboard"

type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]
export type { TableSelection }
const PAGE = 100
/**
 * How many rows an export writes before it stops. The server caps it too — this
 * is the number the menu promises, sent as the request's own limit so the
 * promise and the file cannot drift apart.
 */
const EXPORT_CAP = 100_000

const OP_LABELS: Record<string, string> = {
  eq: "=",
  ne: "≠",
  lt: "<",
  lte: "≤",
  gt: ">",
  gte: "≥",
  contains: "contains",
  prefix: "starts with",
  is_null: "is null",
  not_null: "is not null",
}

/**
 * The Browse tab: a table rail beside a data grid that reads, edits, filters,
 * sorts, imports and reshapes.
 *
 * It is one working region — a rail, a strip, a grid, a footer — sized to the
 * window rather than two framed cards stacked on a page: the grid is what the
 * operator is here to read, and it now takes every row the screen has room
 * for instead of a fixed slice under two headers.
 *
 * Two decisions are worth keeping. Sorting and filtering happen on the server,
 * not over the fetched page — filtering one page of a million-row table is not
 * filtering, it is a trick that looks right until somebody relies on it. And
 * the row count is a button rather than part of every page fetch, because
 * COUNT(*) is a full scan on most engines and paying for it on every page turn
 * would make deep paging progressively slower for a number nobody asked for.
 */
/** A table that points at the one being browsed, and the key that does it. */
export type TableReference = { table: string; fk: DbForeignKey }

/** Where the reader is in a table: the page, the order and the filters, tagged with the table. */
type BrowsePlace = {
  table: string
  offset: number
  sort: { column: string; desc: boolean } | null
  filters: DbFilter[]
  showFilters: boolean
}

const FRESH_PLACE: Omit<BrowsePlace, "table"> = {
  offset: 0,
  sort: null,
  filters: [],
  showFilters: false,
}

export function BrowseTab({
  conn,
  info,
  confirm,
  selection,
  onSelect,
}: {
  conn: DbConnection
  info?: DbDriverInfo
  confirm: ConfirmFn
  selection: TableSelection | null
  // Nullable, because the selected table can stop existing: dropping it has to
  // leave the grid empty rather than showing rows read from a table that is
  // gone, with an Insert button that can only fail.
  onSelect: (sel: TableSelection | null) => void
}) {
  const { can } = useAuth()
  const tableIdentity = JSON.stringify([conn.id, selection?.schema, selection?.table])
  const [count, setCount] = useState<number | null>(null)
  // The place is kept for the tab and tagged with the table it belongs to:
  // coming back finds the same page under the same filters, and a different
  // table starts at the top — including the one a foreign key leads to,
  // which is handed its filter under its own name below.
  const [stored, setStored] = useSessionState<BrowsePlace | null>(
    `databases.${conn.id}.browse.place`,
    null,
  )
  const place: BrowsePlace =
    stored?.table === tableIdentity ? stored : { ...FRESH_PLACE, table: tableIdentity }
  const { offset, sort, filters, showFilters } = place
  const movePlace = (changes: Partial<Omit<BrowsePlace, "table">>, table = tableIdentity) =>
    setStored((current) => ({
      ...(current?.table === table ? current : { ...FRESH_PLACE, table }),
      ...changes,
      table,
    }))
  const setOffset = (next: number | ((prev: number) => number)) =>
    movePlace({ offset: typeof next === "function" ? next(offset) : next })
  const setSort = (
    next: BrowsePlace["sort"] | ((prev: BrowsePlace["sort"]) => BrowsePlace["sort"]),
  ) => movePlace({ sort: typeof next === "function" ? next(sort) : next })
  const setFilters = (next: DbFilter[] | ((prev: DbFilter[]) => DbFilter[])) =>
    movePlace({ filters: typeof next === "function" ? next(filters) : next })
  const setShowFilters = (next: boolean) => movePlace({ showFilters: next })
  const [counting, setCounting] = useState(false)
  const [rowSelection, setRowSelection] = useState<{
    query: string
    result?: QueryResult
    indices: Set<number>
  }>()
  const [editor, setEditor] = useState<{
    tableIdentity: string
    mode: "insert" | "edit"
    initial?: Record<string, unknown>
  } | null>(null)
  const [dialog, setDialog] = useState<
    null | "createTable" | "addColumn" | "createIndex" | "renameTable" | "import"
  >(null)

  const tables = usePoll(
    (signal) => get<DbTable[]>(`/databases/${conn.id}/tables`, { schema: "" }, signal),
    0,
    [conn.id],
  )
  const detail = usePoll(
    (signal) =>
      selection
        ? get<DbTableDetail>(
            `/databases/${conn.id}/table`,
            { schema: selection.schema, table: selection.table },
            signal,
          )
        : Promise.resolve(null as unknown as DbTableDetail),
    0,
    [conn.id, selection?.schema, selection?.table],
  )

  /**
   * Every foreign key in the schema, so the grid can answer the other half of
   * the question it already answers one way: not only "what does this row point
   * at" but "what points at this row". The route has existed since the section
   * was written and nothing called it.
   *
   * It is one request per connection — it walks the catalogue, so it is not
   * cheap on a large schema — and it is only asked for once a table is open.
   * It settles on its own; the grid never waits for it.
   */
  const relations = usePoll(
    (signal) =>
      selection
        ? get<DbRelations>(`/databases/${conn.id}/relations`, undefined, signal)
        : Promise.resolve({} as DbRelations),
    0,
    [conn.id, Boolean(selection)],
  )

  // Only filters with a value (or one of the two null tests) are sent, so a
  // half-typed filter row does not blank the grid while it is being written.
  const activeFilters = useMemo(
    () =>
      filters.filter(
        (f) => f.column && (f.value !== "" || f.op === "is_null" || f.op === "not_null"),
      ),
    [filters],
  )
  const filterParam = activeFilters.length ? JSON.stringify(activeFilters) : undefined
  const selectionQuery = JSON.stringify([
    conn.id,
    selection?.schema,
    selection?.table,
    offset,
    sort,
    filterParam,
  ])
  const rows = usePoll(
    (signal) =>
      selection
        ? get<QueryResult>(
            `/databases/${conn.id}/browse`,
            {
              schema: selection.schema,
              table: selection.table,
              limit: PAGE,
              offset,
              orderBy: sort?.column,
              dir: sort?.desc ? "desc" : undefined,
              filters: filterParam,
            },
            signal,
          )
        : Promise.resolve(null as unknown as QueryResult),
    0,
    [conn.id, selection?.schema, selection?.table, offset, sort?.column, sort?.desc, filterParam],
  )
  // Indices belong to one result snapshot. Sorting, filtering, navigating,
  // or refreshing must require a fresh selection before a mutation.
  const selected =
    rowSelection?.query === selectionQuery && rowSelection.result === rows.data
      ? rowSelection.indices
      : new Set<number>()
  const setSelected = (indices: Set<number>) =>
    setRowSelection({ query: selectionQuery, result: rows.data, indices })

  const select = (t: DbTable) => {
    setOffset(0)
    setSort(null)
    setFilters([])
    setCount(null)
    setSelected(new Set())
    onSelect({ schema: t.schema, table: t.name })
  }

  // Opening another table narrowed to one value. It is the same navigation the
  // rail does, plus a filter — which is why it reuses onSelect rather than
  // inventing a second way to be somewhere. Both directions of a foreign key
  // come through here.
  const openFiltered = (target: TableSelection, column: string, value: unknown) => {
    setSelected(new Set())
    movePlace(
      {
        ...FRESH_PLACE,
        filters: [{ column, op: "eq", value: String(value) }],
        showFilters: true,
      },
      JSON.stringify([conn.id, target.schema, target.table]),
    )
    setCount(null)
    onSelect(target)
  }

  /** Outwards: this row's value, in the table it points at. */
  const followForeignKey = (fk: DbForeignKey, value: unknown) =>
    openFiltered({ schema: fk.refSchema || schema, table: fk.refTable }, fk.refColumns[0], value)

  /** Inwards: the rows of another table that point at this one. */
  const followReference = (ref: TableReference, row: Record<string, unknown>) => {
    const value = row[ref.fk.refColumns[0]]
    if (value === null || value === undefined) return
    // Relations are keyed by table name alone, so the child's schema is
    // recovered from the catalogue rather than assumed to be this one's.
    const child = tables.data?.find((t) => t.name === ref.table)
    openFiltered({ schema: child?.schema ?? schema, table: ref.table }, ref.fk.columns[0], value)
  }

  const pk = detail.data?.primaryKey ?? []
  const canWrite = can("service.control")
  const canEditRows = canWrite && pk.length > 0 && detail.data !== null
  const canDDL = canWrite && (info?.ddl ?? false)
  const table = selection?.table
  const schema = selection?.schema ?? ""
  const current = tables.data?.find((t) => t.name === table && t.schema === schema)

  /**
   * The tables whose rows point at the one being browsed. Composite keys are
   * left out for the same reason the outgoing links are: following one means
   * matching several columns at once, and a filter on the first of them would
   * open a list that is wrong rather than merely incomplete.
   */
  const references: TableReference[] = useMemo(() => {
    if (!table || !relations.data) return []
    const out: TableReference[] = []
    for (const [child, fks] of Object.entries(relations.data)) {
      if (child === table) continue
      for (const fk of fks) {
        if (fk.refTable === table && fk.columns.length === 1 && fk.refColumns.length === 1) {
          out.push({ table: child, fk })
        }
      }
    }
    return out.sort((a, b) => a.table.localeCompare(b.table))
  }, [relations.data, table])

  const reload = () => {
    rows.refresh()
    tables.refresh()
    detail.refresh()
    setCount(null)
    setSelected(new Set())
  }

  // Bulk delete is one confirmation for the whole set rather than one per row.
  // Asking somebody to type the table name eight times is how you teach them to
  // type it without reading, which is the habit the phrase exists to prevent.
  const deleteSelected = () => {
    if (!rows.data || selected.size === 0 || !detail.data) return
    if ([...selected].some((index) => !rows.data?.rows[index])) return
    const keys = [...selected].map((i) => {
      const key: Record<string, unknown> = {}
      for (const c of pk) key[c] = rows.data!.rows[i][rows.data!.columns.indexOf(c)]
      return key
    })
    confirm({
      title: `Delete ${plural(keys.length, "row")}`,
      confirmLabel: `Delete ${plural(keys.length, "row")}`,
      description: (
        <p>
          Permanently deletes <b>{plural(keys.length, "row")}</b> from <b>{table}</b>. This cannot
          be undone.
        </p>
      ),
      action: async (c) => {
        // Sent one at a time so a row that cannot be deleted — a foreign key
        // still pointing at it — names itself, rather than failing the batch
        // with one message about none of them in particular.
        let failed = 0
        for (const key of keys) {
          try {
            await del(`/databases/${conn.id}/rows`, { body: { schema, table, key }, confirm: c })
          } catch {
            failed++
          }
        }
        if (failed) {
          notify.warning(`Deleted ${keys.length - failed}, ${failed} could not be removed`)
        } else {
          notify.success(`Deleted ${plural(keys.length, "row")}`)
        }
        reload()
      },
    })
  }

  /**
   * Copying rows out as INSERT statements is the small thing a developer does
   * constantly and no panel offers: reproducing a production record locally to
   * debug against, seeding a fixture, attaching the offending row to a bug
   * report. The rendering is done on the server so the quoting is this engine's
   * own — a second implementation here would get the apostrophe wrong on the
   * day it mattered.
   */
  const copyAsInsert = async (recs: Record<string, unknown>[]) => {
    if (!table || recs.length === 0) return
    try {
      const res = await post<{ sql: string }>(`/databases/${conn.id}/rows/sql`, {
        schema,
        table,
        rows: recs,
      })
      await copyText(res.sql, `Copied ${plural(recs.length, "row")} as SQL`)
    } catch (err) {
      notify.error("Could not copy", err)
    }
  }

  const copySelectedAsInsert = () => {
    if (!rows.data || selected.size === 0) return
    copyAsInsert(
      [...selected].map((i) => {
        const rec: Record<string, unknown> = {}
        rows.data!.columns.forEach((c, j) => {
          rec[c] = rows.data!.rows[i][j]
        })
        return rec
      }),
    )
  }

  /**
   * Duplicating opens the insert form pre-filled from the row, with the primary
   * key cleared. Carrying the key over would produce a form that can only fail
   * on a unique constraint — and clearing it is what the operator was going to
   * do first anyway.
   */
  const duplicateRow = (row: Record<string, unknown>) => {
    const copy = { ...row }
    for (const c of pk) delete copy[c]
    setEditor({ mode: "insert", initial: copy, tableIdentity })
  }

  const toggleSort = (column: string) => {
    setOffset(0)
    setSort((s) =>
      s?.column === column ? (s.desc ? null : { column, desc: true }) : { column, desc: false },
    )
  }

  const fetchCount = async () => {
    if (!selection) return
    setCounting(true)
    try {
      const res = await get<{ count: number }>(`/databases/${conn.id}/count`, {
        schema,
        table,
        filters: filterParam,
      })
      setCount(res.count)
    } catch (err) {
      notify.error("Could not count rows", err)
    } finally {
      setCounting(false)
    }
  }

  const insertRow = async (values: Record<string, unknown>) => {
    await post(`/databases/${conn.id}/rows`, { schema, table, values })
    notify.success("Row inserted")
    reload()
  }
  const updateRow = async (values: Record<string, unknown>, key?: Record<string, unknown>) => {
    await patch(`/databases/${conn.id}/rows`, { schema, table, values, key })
    notify.success("Row updated")
    reload()
  }
  const deleteRow = (row: Record<string, unknown>) => {
    const key: Record<string, unknown> = {}
    for (const c of pk) key[c] = row[c]
    confirm({
      title: "Delete row",
      confirmLabel: "Delete row",
      description: (
        <p>
          Permanently deletes the row where{" "}
          <span className="font-mono text-xs">
            {pk.map((c) => `${c}=${String(row[c])}`).join(", ")}
          </span>{" "}
          from <b>{table}</b>. This cannot be undone.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/rows`, { body: { schema, table, key }, confirm: c })
        notify.success("Row deleted")
        reload()
      },
    })
  }

  /**
   * The export is the view, not the table. It carries the conditions and the
   * order the grid is under, because the alternative is the failure this page
   * used to have: narrow a million rows to eleven, press Export as CSV, and
   * receive a million-row file that looks exactly like a correct export until
   * somebody opens it. The row cap travels too, so the sentence in the menu
   * and the request are the same number.
   */
  const exportTable = (format: "csv" | "json") => {
    if (!selection) return
    const a = document.createElement("a")
    a.href = downloadUrl(`/databases/${conn.id}/export`, {
      schema,
      table,
      format,
      limit: EXPORT_CAP,
      orderBy: sort?.column,
      dir: sort?.desc ? "desc" : undefined,
      filters: filterParam,
    })
    a.click()
  }

  const dropTable = () =>
    confirm({
      title: "Drop table",
      phrase: table,
      confirmLabel: "Drop table",
      description: (
        <p>
          Permanently destroys <b>{table}</b> and every row in it. This cannot be undone.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/ddl/table`, { body: { schema, table }, confirm: c })
        notify.success(`Dropped ${table}`)
        setCount(null)
        // Deselected before the list is refreshed: the table is gone, and
        // leaving it selected left the grid showing its last rows under a live
        // Insert button while the list beside it had already dropped the name.
        onSelect(null)
        tables.refresh()
      },
    })

  const truncateTable = () =>
    confirm({
      title: "Empty table",
      phrase: table,
      confirmLabel: "Empty it",
      description: (
        <p>
          Removes every row from <b>{table}</b>, keeping the table itself. This cannot be undone.
        </p>
      ),
      action: async (c) => {
        await post(`/databases/${conn.id}/ddl/truncate`, { schema, table }, { confirm: c })
        notify.success(`Emptied ${table}`)
        reload()
      },
    })

  const first = offset + 1
  const last = offset + (rows.data?.rowCount ?? 0)
  const page = Math.floor(offset / PAGE) + 1
  // Only a counted table knows how many pages it has; until then the field
  // still jumps, it just cannot say what the end is.
  const pages = count === null ? null : Math.max(1, Math.ceil(count / PAGE))
  const goPage = (next: number) => {
    const clamped = Math.max(1, pages === null ? next : Math.min(next, pages))
    // Selection is by row index within the page, so it cannot survive a page
    // turn — carrying it would delete whatever now sits at those positions.
    setSelected(new Set())
    setOffset((clamped - 1) * PAGE)
  }
  const exportHint = [
    activeFilters.length > 0
      ? `${plural(activeFilters.length, "condition")} applied`
      : "The whole table",
    sort ? `ordered by ${sort.column}` : null,
    count !== null && count > EXPORT_CAP
      ? `first ${EXPORT_CAP.toLocaleString()} of ${count.toLocaleString()} rows`
      : `up to ${EXPORT_CAP.toLocaleString()} rows`,
  ]
    .filter(Boolean)
    .join(", ")

  return (
    <Pane className="min-h-0 flex-1">
      <div className="grid min-h-0 flex-1 grid-rows-[minmax(0,14rem)_minmax(0,1fr)] lg:grid-cols-[17rem_minmax(0,1fr)] lg:grid-rows-1">
        <TableRail
          connId={conn.id}
          tables={tables.data}
          loading={tables.loading}
          selected={selection}
          onSelect={select}
          className="border-b border-hairline lg:border-r lg:border-b-0"
          action={
            canDDL && (
              <Button
                size="icon-sm"
                variant="outline"
                className="size-7"
                aria-label="Create table"
                title="Create table"
                onClick={() => setDialog("createTable")}
              >
                <Plus className="size-3.5" />
              </Button>
            )
          }
        />

        <div className="flex min-h-0 min-w-0 flex-col">
          <PaneHeader className="gap-2">
            <span className="min-w-0 flex-1 truncate text-body font-medium">
              {table ?? <span className="text-muted-foreground">Pick a table</span>}
            </span>
            {current?.type && current.type.toLowerCase() !== "table" && <Tag>{current.type}</Tag>}
            {table && (
              <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
                {selected.size > 0 && (
                  <>
                    <Button size="sm" variant="outline" onClick={copySelectedAsInsert}>
                      <Copy className="size-3.5" />
                      Copy {selected.size} as SQL
                    </Button>
                    {canEditRows && (
                      <Button size="sm" variant="destructive" onClick={deleteSelected}>
                        <Trash className="size-3.5" />
                        Delete {selected.size}
                      </Button>
                    )}
                  </>
                )}
                <Toggle
                  size="sm"
                  variant="outline"
                  pressed={showFilters}
                  onPressedChange={setShowFilters}
                  aria-label="Filter rows"
                  className="gap-1.5 px-2.5 text-body"
                >
                  <Filter className="size-3.5" />
                  Filter
                  {activeFilters.length > 0 && (
                    <span className="numeric text-hint opacity-70">{activeFilters.length}</span>
                  )}
                </Toggle>
                {canWrite && (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setEditor({ mode: "insert", tableIdentity })}
                  >
                    <Plus className="size-3.5" />
                    Insert
                  </Button>
                )}
                <TableMenu
                  canWrite={canWrite}
                  canDDL={canDDL}
                  counting={counting}
                  onCount={fetchCount}
                  onExport={exportTable}
                  exportHint={exportHint}
                  onImport={() => setDialog("import")}
                  onAddColumn={() => setDialog("addColumn")}
                  onCreateIndex={() => setDialog("createIndex")}
                  onRename={() => setDialog("renameTable")}
                  onTruncate={truncateTable}
                  onDrop={dropTable}
                />
              </div>
            )}
          </PaneHeader>

          {table && showFilters && (
            <div className="shrink-0 space-y-1.5 border-b border-hairline px-3 py-2">
              {filters.map((f, i) => (
                <FilterRow
                  key={i}
                  filter={f}
                  columns={detail.data?.columns.map((c) => c.name) ?? []}
                  ops={info?.filterOps ?? Object.keys(OP_LABELS)}
                  onChange={(patchF) => {
                    setOffset(0)
                    setFilters((fs) => fs.map((x, j) => (j === i ? { ...x, ...patchF } : x)))
                  }}
                  onRemove={() => {
                    setOffset(0)
                    setFilters((fs) => fs.filter((_, j) => j !== i))
                  }}
                />
              ))}
              <div className="flex flex-wrap items-center gap-3">
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() =>
                    setFilters((fs) => [
                      ...fs,
                      { column: detail.data?.columns[0]?.name ?? "", op: "eq", value: "" },
                    ])
                  }
                >
                  <Plus />
                  Add condition
                </Button>
                {activeFilters.length > 0 && (
                  <span className="text-hint text-muted-foreground">
                    Applied on the server, across the whole table — not just this page.
                  </span>
                )}
              </div>
            </div>
          )}

          <div className="relative min-h-0 min-w-0 flex-1">
            {!table && <EmptyState icon={Layout} title="Pick a table to browse its rows" />}
            {table && rows.error && <ErrorState error={rows.error} className="m-4" />}
            {table && !rows.error && !rows.data && <LoadingRows rows={8} className="p-4" />}
            {table && rows.data && (
              <ResultGrid
                // Keyed on the request, so a new page, a new order or a new
                // set of conditions arrives rather than swapping in place.
                key={selectionQuery}
                className="animate-rise"
                result={rows.data}
                sort={sort}
                onSort={toggleSort}
                foreignKeys={detail.data?.foreignKeys}
                onFollow={followForeignKey}
                references={references}
                onFollowReference={followReference}
                // Selecting is a read: it is how rows are copied out as JSON
                // or as INSERTs, which a table with no primary key and a
                // reader with no write capability can both do. Only Delete
                // needs a key and the capability, and it is guarded on its own.
                selection={selected}
                onSelectionChange={setSelected}
                onEdit={
                  canEditRows
                    ? (row) => setEditor({ mode: "edit", initial: row, tableIdentity })
                    : undefined
                }
                onDelete={canEditRows ? deleteRow : undefined}
                onDuplicate={canWrite ? duplicateRow : undefined}
                onCopySQL={copyAsInsert}
                maxHeightClass="h-full"
                emptyTitle={
                  activeFilters.length > 0
                    ? "No rows match these conditions"
                    : "This table is empty"
                }
                emptyDescription={
                  activeFilters.length > 0
                    ? "The conditions are applied on the server, across the whole table — so this is every row, not just this page."
                    : `${table} exists and has its columns, but nothing has been written to it yet.`
                }
              />
            )}
          </div>

          {table && (
            <PaneFooter className="justify-between">
              <div className="flex flex-wrap items-center gap-1.5">
                <Button
                  size="xs"
                  variant="outline"
                  disabled={offset === 0}
                  onClick={() => goPage(1)}
                >
                  First
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={offset === 0}
                  onClick={() => goPage(page - 1)}
                >
                  Previous
                </Button>
                <PageJump page={page} pages={pages} onGo={goPage} />
                <Button
                  size="xs"
                  variant="outline"
                  disabled={rows.data ? rows.data.rowCount < PAGE : true}
                  onClick={() => goPage(page + 1)}
                >
                  Next
                </Button>
                {pages !== null && (
                  <Button
                    size="xs"
                    variant="outline"
                    disabled={page >= pages}
                    onClick={() => goPage(pages)}
                  >
                    Last
                  </Button>
                )}
                {rows.data && rows.data.rowCount > 0 && (
                  <span className="numeric ml-1.5 text-hint text-muted-foreground">
                    Rows {first.toLocaleString()}–{last.toLocaleString()}
                    {count !== null && ` of ${count.toLocaleString()}`}
                    {rows.data.duration && ` · ${rows.data.duration}`}
                  </span>
                )}
              </div>
              <div className="flex items-center gap-3 text-hint text-muted-foreground">
                {canWrite && pk.length === 0 && detail.data && (
                  <span>No primary key — rows cannot be edited here. Use Query with a WHERE.</span>
                )}
                {count === null && rows.data && (
                  <Button size="xs" variant="ghost" onClick={fetchCount} pending={counting}>
                    Count all rows
                  </Button>
                )}
                {/* The grid does not poll: a table being read under an open
                    editor should not shuffle under the pointer. So re-reading
                    it has to be a verb, and before this it was only a side
                    effect of picking the table again. */}
                <Button size="xs" variant="ghost" onClick={reload} pending={rows.loading}>
                  Refresh
                </Button>
              </div>
            </PaneFooter>
          )}
        </div>
      </div>

      {editor && editor.tableIdentity === tableIdentity && detail.data && (
        <RowEditor
          open
          onOpenChange={(o) => !o && setEditor(null)}
          mode={editor.mode}
          table={table}
          columns={detail.data.columns}
          primaryKey={pk}
          initial={editor.initial}
          onSubmit={editor.mode === "insert" ? insertRow : updateRow}
        />
      )}

      {dialog === "createTable" && (
        <CreateTableDialog
          open
          onOpenChange={() => setDialog(null)}
          connId={conn.id}
          schema={schema}
          info={info}
          onDone={reload}
        />
      )}
      {dialog === "addColumn" && table && (
        <AddColumnDialog
          open
          onOpenChange={() => setDialog(null)}
          connId={conn.id}
          schema={schema}
          table={table}
          info={info}
          onDone={reload}
        />
      )}
      {dialog === "createIndex" && table && (
        <CreateIndexDialog
          open
          onOpenChange={() => setDialog(null)}
          connId={conn.id}
          schema={schema}
          table={table}
          detail={detail.data}
          onDone={reload}
        />
      )}
      {dialog === "renameTable" && table && (
        <RenameDialog
          open
          onOpenChange={() => setDialog(null)}
          connId={conn.id}
          schema={schema}
          table={table}
          kind="table"
          current={table}
          onDone={(to) => {
            setCount(null)
            // Follow the rename rather than holding the old name, which the
            // next poll would ask the server for and be told does not exist.
            onSelect({ schema, table: to })
            tables.refresh()
          }}
        />
      )}
      {dialog === "import" && table && (
        <ImportDialog
          open
          onOpenChange={() => setDialog(null)}
          connId={conn.id}
          schema={schema}
          table={table}
          detail={detail.data}
          confirm={confirm}
          onDone={reload}
        />
      )}
    </Pane>
  )
}

/**
 * Which page of the table you are on, as a field you can type into.
 *
 * Previous and Next alone are paging only for the first few hundred rows: an
 * audit table opened at its newest entries is four hundred presses from the
 * thousandth page, and the offset was in session state where nothing could
 * reach it. The field holds its own draft while it is being typed so a
 * half-entered number never re-fetches, and commits on Enter or on blur.
 */
function PageJump({
  page,
  pages,
  onGo,
}: {
  page: number
  pages: number | null
  onGo: (page: number) => void
}) {
  const [draft, setDraft] = useState<string | null>(null)
  const commit = () => {
    const next = Number(draft)
    if (draft !== null && draft !== "" && Number.isFinite(next)) onGo(Math.floor(next))
    setDraft(null)
  }
  return (
    <span className="ml-1 flex items-center gap-1.5 text-hint text-muted-foreground">
      <Input
        value={draft ?? String(page)}
        onChange={(e) => setDraft(e.target.value.replace(/[^0-9]/g, ""))}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") commit()
          if (e.key === "Escape") setDraft(null)
        }}
        inputMode="numeric"
        aria-label="Page"
        className="numeric h-6 w-14 px-1.5 text-center text-xs sm:h-6"
      />
      {pages !== null && <span className="numeric">of {pages.toLocaleString()}</span>}
    </span>
  )
}

function FilterRow({
  filter,
  columns,
  ops,
  onChange,
  onRemove,
}: {
  filter: DbFilter
  columns: string[]
  ops: string[]
  onChange: (patch: Partial<DbFilter>) => void
  onRemove: () => void
}) {
  const needsValue = filter.op !== "is_null" && filter.op !== "not_null"
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <Select value={filter.column} onValueChange={(v) => onChange({ column: v })}>
        <SelectTrigger size="sm" className="h-7 w-44 text-xs sm:h-7" aria-label="Column">
          <SelectValue placeholder="column" />
        </SelectTrigger>
        <SelectContent>
          {columns.map((c) => (
            <SelectItem key={c} value={c} className="font-mono text-xs">
              {c}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Select value={filter.op} onValueChange={(v) => onChange({ op: v })}>
        <SelectTrigger size="sm" className="h-7 w-32 text-xs sm:h-7" aria-label="Condition">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {ops.map((o) => (
            <SelectItem key={o} value={o}>
              {OP_LABELS[o] ?? o}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Input
        value={filter.value}
        onChange={(e) => onChange({ value: e.target.value })}
        disabled={!needsValue}
        className="h-7 max-w-xs font-mono text-xs sm:h-7"
        placeholder={needsValue ? "value" : ""}
        aria-label="Value"
      />
      <Button
        size="icon-xs"
        variant="ghost"
        aria-label="Remove this condition"
        onClick={onRemove}
        className="text-muted-foreground"
      >
        <Cross />
      </Button>
    </div>
  )
}
