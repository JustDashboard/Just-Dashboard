"use client"

import { useMemo, useState } from "react"
import {
  AcronymJson,
  CloudUpload,
  Copy,
  Cross,
  Download,
  Filter,
  Hash,
  Layout,
  MoreHorizontal,
  Plus,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, downloadUrl, get, patch, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { cn, ringSafeScroll } from "@/lib/utils"
import type {
  DbConnection,
  DbDriverInfo,
  DbFilter,
  DbForeignKey,
  DbTable,
  DbTableDetail,
  QueryResult,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { ResultGrid } from "@/components/database/result-grid"
import { RowEditor } from "@/components/database/row-editor"
import { ImportDialog } from "@/components/database/import-dialog"
import {
  AddColumnDialog,
  CreateIndexDialog,
  CreateTableDialog,
  RenameDialog,
} from "@/components/database/ddl-dialogs"
import { copyText } from "@/lib/clipboard"

type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]
export type TableSelection = { schema: string; table: string }
const PAGE = 100

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
 * Two decisions are worth keeping. Sorting and filtering happen on the server,
 * not over the fetched page — filtering one page of a million-row table is not
 * filtering, it is a trick that looks right until somebody relies on it. And
 * the row count is a button rather than part of every page fetch, because
 * COUNT(*) is a full scan on most engines and paying for it on every page turn
 * would make deep paging progressively slower for a number nobody asked for.
 */
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
  const [textFilter, setTextFilter] = useState("")
  const [schemaFilter, setSchemaFilter] = useState("all")
  const [count, setCount] = useState<number | null>(null)
  const [offset, setOffset] = useState(0)
  const [sort, setSort] = useState<{ column: string; desc: boolean } | null>(null)
  const [filters, setFilters] = useState<DbFilter[]>([])
  const [showFilters, setShowFilters] = useState(false)
  const [counting, setCounting] = useState(false)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [editor, setEditor] = useState<{
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

  const schemaNames = useMemo(() => {
    const set = new Set<string>()
    for (const t of tables.data ?? []) set.add(t.schema)
    return [...set].sort()
  }, [tables.data])

  const visibleTables = useMemo(() => {
    let list = tables.data ?? []
    if (schemaFilter !== "all") list = list.filter((t) => t.schema === schemaFilter)
    const q = textFilter.trim().toLowerCase()
    if (q) list = list.filter((t) => t.name.toLowerCase().includes(q))
    return list
  }, [tables.data, schemaFilter, textFilter])

  const select = (t: DbTable) => {
    setOffset(0)
    setSort(null)
    setFilters([])
    setCount(null)
    setSelected(new Set())
    onSelect({ schema: t.schema, table: t.name })
  }

  // Following a foreign key opens the parent table filtered to the referenced
  // row. It is the same navigation the rail does, plus a filter — which is why
  // it reuses onSelect rather than inventing a second way to be somewhere.
  const followForeignKey = (fk: DbForeignKey, value: unknown) => {
    setOffset(0)
    setSort(null)
    setSelected(new Set())
    setFilters([{ column: fk.refColumns[0], op: "eq", value: String(value) }])
    setShowFilters(true)
    setCount(null)
    onSelect({ schema: fk.refSchema || schema, table: fk.refTable })
  }

  const pk = detail.data?.primaryKey ?? []
  const canWrite = can("service.control")
  const canEditRows = canWrite && pk.length > 0 && detail.data !== null
  const canDDL = canWrite && (info?.ddl ?? false)
  const table = selection?.table
  const schema = selection?.schema ?? ""

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
    if (!rows.data || selected.size === 0) return
    const keys = [...selected].map((i) => {
      const key: Record<string, unknown> = {}
      for (const c of pk) key[c] = rows.data!.rows[i][rows.data!.columns.indexOf(c)]
      return key
    })
    confirm({
      title: `Delete ${plural(keys.length, "row")}`,
      confirmLabel: `Delete ${plural(keys.length, "row")}`,
      description: (
        <p className="text-sm">
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
    setEditor({ mode: "insert", initial: copy })
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
        <p className="text-sm">
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

  const exportTable = (format: "csv" | "json") => {
    if (!selection) return
    const a = document.createElement("a")
    a.href = downloadUrl(`/databases/${conn.id}/export`, { schema, table, format })
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

  return (
    <div className="grid gap-4 lg:grid-cols-[16rem_minmax(0,1fr)] [&>*]:min-w-0">
      <Panel>
        <PanelHeader
          title="Tables"
          actions={
            canDDL && (
              <Button size="sm" variant="outline" onClick={() => setDialog("createTable")}>
                <Plus className="size-3.5" />
                New
              </Button>
            )
          }
        />
        <PanelBody className="space-y-3">
          {schemaNames.length > 1 && (
            <Select value={schemaFilter} onValueChange={setSchemaFilter}>
              <SelectTrigger size="sm" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All schemas</SelectItem>
                {schemaNames.map((s) => (
                  <SelectItem key={s} value={s}>
                    {s}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <Input
            placeholder="Filter tables…"
            value={textFilter}
            onChange={(e) => setTextFilter(e.target.value)}
            className="h-8 text-xs"
          />
          <div
            className={cn("max-h-[calc(100svh-28rem)] space-y-0.5 overflow-y-auto", ringSafeScroll)}
          >
            {visibleTables.map((t) => (
              <button
                key={`${t.schema}.${t.name}`}
                onClick={() => select(t)}
                className={cn(
                  "flex w-full min-w-0 flex-col rounded-md px-2 py-1.5 text-left transition-colors",
                  table === t.name && selection?.schema === t.schema
                    ? "bg-plot-primary font-medium text-foreground"
                    : "hover:bg-accent",
                )}
              >
                <span className="truncate text-body">{t.name}</span>
                <span className="truncate text-hint text-muted-foreground">
                  {schemaNames.length > 1 ? `${t.schema} · ` : ""}
                  {t.type}
                  {t.estimatedRows > 0 && ` · ${plural(t.estimatedRows, "row")}`}
                  {t.size ? ` · ${bytes(t.size)}` : ""}
                </span>
              </button>
            ))}
            {tables.loading && <LoadingRows rows={4} />}
            {tables.data?.length === 0 && <EmptyNote>No tables found.</EmptyNote>}
            {tables.data && tables.data.length > 0 && visibleTables.length === 0 && (
              <EmptyNote>No tables match the filter.</EmptyNote>
            )}
          </div>
        </PanelBody>
      </Panel>

      <Panel>
        <PanelHeader
          title={table ?? "Pick a table"}
          actions={
            table && (
              <>
                {canWrite && (
                  <Button size="sm" variant="outline" onClick={() => setEditor({ mode: "insert" })}>
                    <Plus className="size-3.5" />
                    Insert
                  </Button>
                )}
                {selected.size > 0 && (
                  <Button size="sm" variant="outline" onClick={copySelectedAsInsert}>
                    <Copy className="size-3.5" />
                    Copy {selected.size} as SQL
                  </Button>
                )}
                {canEditRows && selected.size > 0 && (
                  <Button size="sm" variant="destructive" onClick={deleteSelected}>
                    <Trash className="size-3.5" />
                    Delete {selected.size}
                  </Button>
                )}
                <Button
                  size="sm"
                  variant={showFilters ? "default" : "ghost"}
                  onClick={() => setShowFilters((v) => !v)}
                >
                  <Filter className="size-3.5" />
                  Filter
                  {activeFilters.length > 0 && ` (${activeFilters.length})`}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={offset === 0}
                  onClick={() => {
                    // Selection is by row index within the page, so it cannot
                    // survive a page turn — carrying it would delete whatever
                    // now sits at those positions.
                    setSelected(new Set())
                    setOffset((o) => Math.max(0, o - PAGE))
                  }}
                >
                  Previous
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={rows.data ? rows.data.rowCount < PAGE : true}
                  onClick={() => {
                    setSelected(new Set())
                    setOffset((o) => o + PAGE)
                  }}
                >
                  Next
                </Button>
                {count !== null && (
                  // The counted total, where it was asked for. It used to be a
                  // sentence under the panel's title; the number is the whole
                  // content of that sentence.
                  <span className="numeric text-hint text-muted-foreground">
                    {count.toLocaleString()} rows
                  </span>
                )}
                <TableMenu
                  canWrite={canWrite}
                  canDDL={canDDL}
                  counting={counting}
                  onCount={fetchCount}
                  onExport={exportTable}
                  onImport={() => setDialog("import")}
                  onAddColumn={() => setDialog("addColumn")}
                  onCreateIndex={() => setDialog("createIndex")}
                  onRename={() => setDialog("renameTable")}
                  onTruncate={truncateTable}
                  onDrop={dropTable}
                />
              </>
            )
          }
        />

        {table && showFilters && (
          <PanelToolbar className="flex-col items-stretch gap-1.5">
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
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  setFilters((fs) => [
                    ...fs,
                    { column: detail.data?.columns[0]?.name ?? "", op: "eq", value: "" },
                  ])
                }
              >
                <Plus className="size-3.5" />
                Add condition
              </Button>
              {activeFilters.length > 0 && (
                <span className="text-hint text-muted-foreground">
                  Applied on the server, across the whole table — not just this page.
                </span>
              )}
            </div>
          </PanelToolbar>
        )}

        <PanelBody flush>
          {rows.error && <ErrorState error={rows.error} className="m-4" />}
          {!table && <EmptyState icon={Layout} title="Select a table to browse" />}
          {table && rows.data && (
            <>
              {canWrite && pk.length === 0 && detail.data && (
                <p className="border-b border-hairline bg-muted/30 px-4 py-1.5 text-hint text-muted-foreground">
                  This table has no primary key, so rows cannot be edited individually. Use the
                  Query tab with an explicit WHERE clause.
                </p>
              )}
              <ResultGrid
                result={rows.data}
                sort={sort}
                onSort={toggleSort}
                foreignKeys={detail.data?.foreignKeys}
                onFollow={followForeignKey}
                selection={canEditRows ? selected : undefined}
                onSelectionChange={canEditRows ? setSelected : undefined}
                onEdit={
                  canEditRows ? (row) => setEditor({ mode: "edit", initial: row }) : undefined
                }
                onDelete={canEditRows ? deleteRow : undefined}
                onDuplicate={canWrite ? duplicateRow : undefined}
                onCopySQL={copyAsInsert}
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
            </>
          )}
        </PanelBody>
      </Panel>

      {editor && detail.data && (
        <RowEditor
          open
          onOpenChange={(o) => !o && setEditor(null)}
          mode={editor.mode}
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
          schema={schemaFilter === "all" ? "" : schemaFilter}
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
    </div>
  )
}

function TableMenu({
  canWrite,
  canDDL,
  counting,
  onCount,
  onExport,
  onImport,
  onAddColumn,
  onCreateIndex,
  onRename,
  onTruncate,
  onDrop,
}: {
  canWrite: boolean
  canDDL: boolean
  counting: boolean
  onCount: () => void
  onExport: (f: "csv" | "json") => void
  onImport: () => void
  onAddColumn: () => void
  onCreateIndex: () => void
  onRename: () => void
  onTruncate: () => void
  onDrop: () => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm" variant="ghost" pending={counting}>
          <MoreHorizontal className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuItem onClick={onCount}>
          <Hash className="size-3.5" />
          Count rows
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => onExport("csv")}>
          <Download className="size-3.5" />
          Export CSV
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => onExport("json")}>
          <AcronymJson className="size-3.5" />
          Export JSON
        </DropdownMenuItem>
        {canWrite && (
          <DropdownMenuItem onClick={onImport}>
            <CloudUpload className="size-3.5" />
            Import data…
          </DropdownMenuItem>
        )}
        {canDDL && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={onAddColumn}>
              <Plus className="size-3.5" />
              Add column…
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onCreateIndex}>
              <Plus className="size-3.5" />
              Create index…
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onRename}>Rename table…</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onClick={onTruncate}>
              <Trash className="size-3.5" />
              Empty table…
            </DropdownMenuItem>
            <DropdownMenuItem variant="destructive" onClick={onDrop}>
              <Trash className="size-3.5" />
              Drop table…
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
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
    <div className="flex items-center gap-1.5">
      <Select value={filter.column} onValueChange={(v) => onChange({ column: v })}>
        <SelectTrigger size="sm" className="w-44">
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
        <SelectTrigger size="sm" className="w-32">
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
        className="h-8 max-w-xs font-mono text-xs"
        placeholder={needsValue ? "value" : ""}
      />
      <Button
        size="icon"
        variant="ghost"
        className="size-7"
        aria-label="Remove this filter"
        onClick={onRemove}
      >
        <Cross className="size-3.5" />
      </Button>
    </div>
  )
}
