"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import {
  ChevronDown,
  ChevronRight,
  Link,
  MoreHorizontal,
  Pencil,
  RefreshClockwise,
  Table,
  Trash,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { PollState } from "@/hooks/use-poll"
import { IconAction, rowReveal } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { EmptyNote, EmptyState } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu, type Verb } from "@/components/verbs"
import { KindGlyph, rowObjectKind } from "@/components/database/data/kinds"
import { nameHue } from "@/components/database/home/kinds"
import { ReadError } from "@/components/database/redis/read-error"
import { useDatabase } from "@/components/database/shell/database-context"
import type { SchemaModel, SchemaTable } from "@/components/database/query/completion"
import { focusSoon } from "@/components/database/query/focus"
import { StatementLine } from "@/components/database/query/messages"
import { spanText } from "@/components/database/query/run-model"
import { SNIPPET_GROUPS, type Snippet } from "@/components/database/query/snippets"
import type {
  CatalogHead,
  ColumnInfo,
  HistoryEntry,
  SavedQuery,
} from "@/components/database/query/types"
import { readColumns } from "@/components/database/query/use-query-data"

export type RailView = "saved" | "history" | "schema" | "snippets"

const VIEWS: { id: RailView; label: string }[] = [
  { id: "saved", label: "Saved" },
  { id: "history", label: "History" },
  { id: "schema", label: "Schema" },
  { id: "snippets", label: "Snippets" },
]

/** How many rows of a long list are drawn before the reader asks for more. */
const STEP = 200

/**
 * The editor's rail: what has been kept, what has been run, what the
 * connection holds, and the diagnostics written for its engine.
 *
 * Everything in it leads into the editor and nothing in it replaces what the
 * reader is writing: a saved query, a history entry and a snippet open in a
 * tab, and a name from the schema is written at the cursor.
 */
export function QueryRail({
  view,
  onView,
  saved,
  history,
  schema,
  snippets,
  canWrite,
  openSaved,
  onOpenSaved,
  onRenameSaved,
  onDeleteSaved,
  onOpenStatement,
  onInsert,
  onOpenTable,
  picked,
  onPickSchema,
}: {
  view: RailView
  onView: (view: RailView) => void
  saved: PollState<SavedQuery[]>
  history: PollState<HistoryEntry[]>
  schema: {
    model: SchemaModel
    head: CatalogHead | undefined
    truncated: boolean
    loading: boolean
    error: Error | undefined
    refresh: () => void
  }
  snippets: Snippet[]
  /** The role may keep, rename and delete saved queries. */
  canWrite: boolean
  /** The saved query the tab in front is the draft of. */
  openSaved: number | undefined
  onOpenSaved: (query: SavedQuery) => void
  onRenameSaved: (query: SavedQuery, name: string) => Promise<void>
  /** Asks, then deletes. `closed` hears that the question has gone, and whether the query went with it. */
  onDeleteSaved: (query: SavedQuery, closed: (deleted: boolean) => void) => void
  /** Opens a statement in a tab of its own. */
  onOpenStatement: (sql: string, title?: string) => void
  /** Writes a name at the cursor. */
  onInsert: (text: string) => void
  /** Opens a table's first rows as a statement. */
  onOpenTable: (table: SchemaTable) => void
  /** The schema the tree lists. */
  picked: string
  onPickSchema: (schema: string) => void
}) {
  const tabsId = useId()
  const tabs = useRef<HTMLDivElement>(null)
  const onTabKey = (event: React.KeyboardEvent) => {
    const at = VIEWS.findIndex((entry) => entry.id === view)
    const to =
      event.key === "ArrowRight"
        ? (at + 1) % VIEWS.length
        : event.key === "ArrowLeft"
          ? (at + VIEWS.length - 1) % VIEWS.length
          : -1
    if (to < 0) return
    event.preventDefault()
    onView(VIEWS[to].id)
    tabs.current?.querySelector<HTMLElement>(`[data-view="${VIEWS[to].id}"]`)?.focus()
  }

  return (
    <div data-slot="query-rail" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div
        ref={tabs}
        role="tablist"
        aria-label="Saved queries, history, schema and snippets"
        className="flex h-10 shrink-0 [scrollbar-width:none] items-stretch overflow-x-auto border-b border-hairline bg-surface-header [&::-webkit-scrollbar]:hidden"
        onKeyDown={onTabKey}
      >
        {VIEWS.map((entry) => {
          const selected = view === entry.id
          return (
            <button
              key={entry.id}
              type="button"
              role="tab"
              id={`${tabsId}-${entry.id}`}
              data-view={entry.id}
              aria-selected={selected}
              aria-controls={selected ? `${tabsId}-panel` : undefined}
              tabIndex={selected ? 0 : -1}
              className={cn(tabClasses(selected, "h-10"), "px-2.5")}
              onClick={() => onView(entry.id)}
            >
              {entry.label}
              {entry.id === "saved" && saved.data && saved.data.length > 0 && (
                <ChipCount>{saved.data.length}</ChipCount>
              )}
            </button>
          )
        })}
      </div>
      <div
        role="tabpanel"
        id={`${tabsId}-panel`}
        aria-labelledby={`${tabsId}-${view}`}
        className="flex min-h-0 flex-1 flex-col"
      >
        {view === "saved" && (
          <SavedList
            saved={saved}
            canWrite={canWrite}
            current={openSaved}
            onOpen={onOpenSaved}
            onRename={onRenameSaved}
            onDelete={onDeleteSaved}
          />
        )}
        {view === "history" && <HistoryList history={history} onOpen={onOpenStatement} />}
        {view === "schema" && (
          <SchemaTree
            schema={schema}
            picked={picked}
            onPick={onPickSchema}
            onInsert={onInsert}
            onOpenTable={onOpenTable}
          />
        )}
        {view === "snippets" && <SnippetList snippets={snippets} onOpen={onOpenStatement} />}
      </div>
    </div>
  )
}

/* ---------------------------------------------------------------- shared */

function RailSearch({
  value,
  onChange,
  label,
  trailing,
}: {
  value: string
  onChange: (value: string) => void
  label: string
  trailing?: React.ReactNode
}) {
  return (
    <div className="flex shrink-0 items-center gap-1 border-b border-hairline p-1.5">
      <SearchInput
        dense
        value={value}
        placeholder={label}
        aria-label={label}
        containerClassName="sm:w-full"
        onChange={(event) => onChange(event.target.value)}
      />
      {trailing}
    </div>
  )
}

function RailSkeleton({ lines = 2 }: { lines?: 1 | 2 }) {
  const widths = ["w-32", "w-24", "w-36", "w-28", "w-20", "w-32"]
  return (
    <div aria-hidden className="space-y-3 px-3 pt-3">
      {widths.map((width, index) => (
        <div key={index} className="space-y-1.5">
          <Skeleton className={cn("h-3", width)} />
          {lines === 2 && <Skeleton className="h-2.5 w-44 max-w-full" />}
        </div>
      ))}
    </div>
  )
}

function NothingMatches({ onClear }: { onClear: () => void }) {
  return (
    <div className="px-2 py-6 text-center">
      <p className="text-body text-muted-foreground">Nothing here is called that.</p>
      <Button size="xs" variant="ghost" className="mt-2" onClick={onClear}>
        Clear the search
      </Button>
    </div>
  )
}

/* ----------------------------------------------------------------- saved */

function SavedList({
  saved,
  canWrite,
  current,
  onOpen,
  onRename,
  onDelete,
}: {
  saved: PollState<SavedQuery[]>
  canWrite: boolean
  current: number | undefined
  onOpen: (query: SavedQuery) => void
  onRename: (query: SavedQuery, name: string) => Promise<void>
  onDelete: (query: SavedQuery, closed: (deleted: boolean) => void) => void
}) {
  const { href } = useDatabase()
  const [find, setFind] = useState("")
  const [renaming, setRenaming] = useState<number | null>(null)
  const rows = useRef<HTMLUListElement>(null)
  // Where the keyboard goes when the control it was on has gone: to the row
  // the reader was working on, else to the one beside it, else to the list's tab.
  const focusRow = (...ids: (number | undefined)[]) =>
    focusSoon(
      () => {
        for (const id of ids) {
          const row = rows.current?.querySelector<HTMLElement>(`[data-saved="${id}"]`)
          if (row) return row
        }
        return null
      },
      () =>
        document.querySelector<HTMLElement>(
          '[data-slot=query-rail] [role="tab"][aria-selected="true"]',
        ),
    )
  const needle = find.trim().toLowerCase()
  const list = useMemo(() => {
    const all = [...(saved.data ?? [])].sort((a, b) => a.name.localeCompare(b.name))
    return needle
      ? all.filter(
          (query) =>
            query.name.toLowerCase().includes(needle) || query.sql.toLowerCase().includes(needle),
        )
      : all
  }, [saved.data, needle])

  if (!saved.data) {
    return saved.error ? (
      <ReadError error={saved.error} onRetry={saved.refresh} className="m-2" />
    ) : (
      <RailSkeleton />
    )
  }
  if (saved.data.length === 0) {
    return (
      <EmptyState
        className="mt-2 border-0 px-3 py-8"
        title="Nothing saved yet"
        description={
          canWrite
            ? "Save keeps the statement in the editor under a name, for this connection and everybody who opens it."
            : "Nobody has kept a statement for this connection."
        }
      />
    )
  }
  return (
    <>
      <RailSearch value={find} onChange={setFind} label="Find a saved query" />
      <ul ref={rows} aria-label="Saved queries" className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {list.length === 0 && <NothingMatches onClear={() => setFind("")} />}
        {list.map((query, at) => {
          const selected = query.id === current
          const link: Verb = {
            key: "link",
            label: "Copy link",
            icon: Link,
            run: () =>
              void copyText(
                `${window.location.origin}${href("query", { saved: String(query.id) })}`,
                "Link copied",
              ),
          }
          const verbs: Verb[] = [
            link,
            { key: "rename", label: "Rename", icon: Pencil, run: () => setRenaming(query.id) },
            {
              key: "delete",
              label: "Delete",
              icon: Trash,
              danger: true,
              run: () =>
                onDelete(query, (deleted) =>
                  deleted ? focusRow(list[at + 1]?.id, list[at - 1]?.id) : focusRow(query.id),
                ),
            },
          ]
          return (
            <li
              key={query.id}
              data-current={selected || undefined}
              className={cn(
                "group flex min-h-11 items-center rounded-md transition-colors",
                selected ? "bg-accent" : "hover:bg-row-hover",
              )}
            >
              {renaming === query.id ? (
                <RenameField
                  name={query.name}
                  onCancel={() => {
                    setRenaming(null)
                    focusRow(query.id)
                  }}
                  onSave={async (name) => {
                    await onRename(query, name)
                    setRenaming(null)
                    focusRow(query.id)
                  }}
                />
              ) : (
                <>
                  <button
                    type="button"
                    aria-label={`Open ${query.name}`}
                    data-saved={query.id}
                    aria-current={selected ? "true" : undefined}
                    onClick={() => onOpen(query)}
                    className="flex min-w-0 flex-1 flex-col gap-0.5 rounded-md px-2 py-1.5 text-left focus-ring-inset"
                  >
                    <span className="truncate text-xs font-medium">{query.name}</span>
                    <StatementLine sql={query.sql} className="text-hint text-muted-foreground" />
                  </button>
                  <VerbMenu
                    verbs={canWrite ? verbs : [link]}
                    label={`Actions for ${query.name}`}
                    trigger={
                      <Button
                        size="icon-xs"
                        variant="ghost"
                        aria-label={`Actions for ${query.name}`}
                        className={cn("mr-1 shrink-0 max-sm:size-8", rowReveal())}
                      >
                        <MoreHorizontal className="size-3.5" />
                      </Button>
                    }
                  />
                </>
              )}
            </li>
          )
        })}
      </ul>
    </>
  )
}

/** A saved query's name, being changed where it stands. Enter keeps it, Escape leaves it. */
function RenameField({
  name,
  onSave,
  onCancel,
}: {
  name: string
  onSave: (name: string) => Promise<void>
  onCancel: () => void
}) {
  const [value, setValue] = useState(name)
  const [busy, setBusy] = useState(false)
  const typed = value.trim()
  const submit = async () => {
    if (busy) return
    if (!typed || typed === name) return onCancel()
    setBusy(true)
    try {
      await onSave(typed)
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      className="flex min-w-0 flex-1 items-center gap-1 p-1"
      onSubmit={(event) => {
        event.preventDefault()
        void submit()
      }}
    >
      <Input
        autoFocus
        value={value}
        maxLength={200}
        aria-label="Name of the saved query"
        disabled={busy}
        className="h-8 min-w-0 flex-1 px-2 sm:h-8 sm:text-xs"
        onChange={(event) => setValue(event.target.value)}
        onFocus={(event) => event.target.select()}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault()
            onCancel()
          }
        }}
      />
      <Button type="submit" size="xs" pending={busy} disabled={!typed}>
        Rename
      </Button>
    </form>
  )
}

/* --------------------------------------------------------------- history */

function HistoryList({
  history,
  onOpen,
}: {
  history: PollState<HistoryEntry[]>
  onOpen: (sql: string) => void
}) {
  const [find, setFind] = useState("")
  const [limit, setLimit] = useState(STEP)
  const needle = find.trim().toLowerCase()
  const list = useMemo(
    () =>
      needle
        ? (history.data ?? []).filter(
            (entry) =>
              entry.sql.toLowerCase().includes(needle) ||
              (entry.error ?? "").toLowerCase().includes(needle),
          )
        : (history.data ?? []),
    [history.data, needle],
  )

  if (!history.data) {
    return history.error ? (
      <ReadError error={history.error} onRetry={history.refresh} className="m-2" />
    ) : (
      <RailSkeleton />
    )
  }
  if (history.data.length === 0) {
    return (
      <EmptyState
        className="mt-2 border-0 px-3 py-8"
        title="Nothing has been run"
        description="Every statement run from this editor is listed here, with how long it took and how it ended."
      />
    )
  }
  return (
    <>
      <RailSearch
        value={find}
        onChange={setFind}
        label="Find in the history"
        trailing={
          <IconAction
            label="Read the history again"
            className="size-7 shrink-0"
            onClick={history.refresh}
          >
            <RefreshClockwise />
          </IconAction>
        }
      />
      <ul aria-label="Statements that were run" className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {list.length === 0 && <NothingMatches onClear={() => setFind("")} />}
        {list.slice(0, limit).map((entry) => (
          <li key={entry.id}>
            <button
              type="button"
              aria-label={`Open in a tab: ${entry.sql.slice(0, 80)}`}
              title={entry.error ? `${entry.sql}\n\n${entry.error}` : entry.sql}
              onClick={() => onOpen(entry.sql)}
              className="flex min-h-11 w-full min-w-0 flex-col justify-center gap-0.5 rounded-md px-2 py-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover"
            >
              <StatementLine sql={entry.sql} />
              <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
                {!entry.success && (
                  <span className="flex shrink-0 items-center gap-1 text-destructive">
                    <StatusDot tone="danger" />
                    failed
                  </span>
                )}
                <span className="shrink-0" title={timestamp(entry.ranAt)}>
                  {relativeTime(entry.ranAt)}
                </span>
                <span className="numeric shrink-0">{spanText(entry.durationMs)}</span>
                {entry.success && (
                  <span className="numeric min-w-0 truncate">
                    {(entry.rowsAffected ?? 0) > 0 && entry.rowCount === 0
                      ? `${entry.rowsAffected!.toLocaleString("en-US")} changed`
                      : `${entry.rowCount.toLocaleString("en-US")} ${entry.rowCount === 1 ? "row" : "rows"}`}
                  </span>
                )}
              </span>
            </button>
          </li>
        ))}
        {list.length > limit && (
          <li>
            <Button
              size="xs"
              variant="ghost"
              className="mt-1 ml-1 text-muted-foreground"
              onClick={() => setLimit((n) => n + STEP)}
            >
              Show {Math.min(STEP, list.length - limit)} more
            </Button>
          </li>
        )}
      </ul>
    </>
  )
}

/* ---------------------------------------------------------------- schema */

function SchemaTree({
  schema,
  picked,
  onPick,
  onInsert,
  onOpenTable,
}: {
  schema: {
    model: SchemaModel
    head: CatalogHead | undefined
    truncated: boolean
    loading: boolean
    error: Error | undefined
    refresh: () => void
  }
  picked: string
  onPick: (schema: string) => void
  onInsert: (text: string) => void
  onOpenTable: (table: SchemaTable) => void
}) {
  const { engine } = useDatabase()
  const [find, setFind] = useState("")
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const [limit, setLimit] = useState(STEP)
  const needle = find.trim().toLowerCase()
  const { model } = schema
  const shown = picked || model.defaultSchema
  const tables = useMemo(() => {
    const inSchema = model.tables.filter((table) => !shown || table.schema === shown)
    if (!needle) return inSchema
    return inSchema.filter(
      (table) =>
        table.name.toLowerCase().includes(needle) ||
        table.columns.some((column) => column.toLowerCase().includes(needle)),
    )
  }, [model.tables, shown, needle])

  const toggle = (key: string) =>
    setOpen((held) => {
      const next = new Set(held)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })

  const schemas = schema.head?.schemas ?? []
  return (
    <>
      <div className="flex shrink-0 items-center gap-1 border-b border-hairline p-1.5">
        {engine.can("schemas") && schemas.length > 1 ? (
          <SchemaPicker schemas={schemas} current={shown} onPick={onPick} />
        ) : (
          <span className="min-w-0 flex-1 truncate px-1 font-mono text-xs text-muted-foreground">
            {shown || engine.nouns.objects}
          </span>
        )}
        <IconAction
          label={`Read the ${engine.nouns.objects} again`}
          className="size-7 shrink-0"
          onClick={schema.refresh}
        >
          <RefreshClockwise />
        </IconAction>
      </div>
      <RailSearch
        value={find}
        onChange={setFind}
        label={`Find a ${engine.nouns.object} or a column`}
      />
      <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {schema.error && model.tables.length === 0 ? (
          <ReadError error={schema.error} onRetry={schema.refresh} className="m-1" />
        ) : schema.loading && model.tables.length === 0 ? (
          <RailSkeleton lines={1} />
        ) : tables.length === 0 ? (
          needle ? (
            <NothingMatches onClear={() => setFind("")} />
          ) : (
            <EmptyState
              className="mt-2 border-0 px-3 py-8"
              icon={Table}
              title={`No ${engine.nouns.objects} in ${shown || `this ${engine.nouns.container}`}`}
              description={`Nothing here holds rows yet. A statement that makes a ${engine.nouns.object} will list it once the list is read again.`}
            />
          )
        ) : (
          <ul aria-label={`${engine.nouns.objects} and their columns`}>
            {tables.slice(0, limit).map((table) => {
              const key = `${table.schema}\u0000${table.name}`
              // A search that matched a column opens its table to show which.
              const matchedColumn =
                needle !== "" &&
                !table.name.toLowerCase().includes(needle) &&
                table.columns.some((column) => column.toLowerCase().includes(needle))
              return (
                <TableNode
                  key={key}
                  table={table}
                  open={open.has(key) || matchedColumn}
                  needle={needle}
                  onToggle={() => toggle(key)}
                  onInsert={onInsert}
                  onOpenTable={onOpenTable}
                />
              )
            })}
          </ul>
        )}
        {tables.length > limit && (
          <Button
            size="xs"
            variant="ghost"
            className="mt-1 ml-1 text-muted-foreground"
            onClick={() => setLimit((n) => n + STEP)}
          >
            Show {Math.min(STEP, tables.length - limit)} more of{" "}
            {tables.length.toLocaleString("en-US")}
          </Button>
        )}
        {schema.truncated && (
          <EmptyNote className="px-2 py-2 text-left text-hint">
            The list was cut at the server&rsquo;s limit: not every {engine.nouns.object} is here.
          </EmptyNote>
        )}
      </div>
    </>
  )
}

function TableNode({
  table,
  open,
  needle,
  onToggle,
  onInsert,
  onOpenTable,
}: {
  table: SchemaTable
  open: boolean
  needle: string
  onToggle: () => void
  onInsert: (text: string) => void
  onOpenTable: (table: SchemaTable) => void
}) {
  const { id } = useDatabase()
  // The outline names the columns; what each one is is read when the table is opened.
  const [types, setTypes] = useState<Record<string, ColumnInfo>>()
  useEffect(() => {
    if (!open || types) return
    const controller = new AbortController()
    readColumns(id, table.schema, table.name, controller.signal)
      .then((columns) =>
        setTypes(Object.fromEntries(columns.map((column) => [column.name, column]))),
      )
      .catch(() => {
        // The names are already drawn; a type that could not be read is left out.
      })
    return () => controller.abort()
  }, [open, types, id, table.schema, table.name])

  const kind = rowObjectKind(table.type)
  return (
    <li>
      <div className="group flex h-7 items-center rounded-md transition-colors hover:bg-row-hover max-sm:h-9">
        <button
          type="button"
          aria-expanded={open}
          aria-label={`${open ? "Hide" : "Show"} the columns of ${table.name}`}
          onClick={onToggle}
          className="flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground focus-ring-inset max-sm:size-8"
        >
          {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
        </button>
        <button
          type="button"
          aria-label={`Write ${table.name} at the cursor`}
          title={`${table.type || "table"} — press to write its name at the cursor`}
          onClick={() => onInsert(table.name)}
          className="flex h-full min-w-0 flex-1 items-center gap-1.5 rounded-md pr-1 text-left focus-ring-inset"
        >
          <KindGlyph kind={kind} />
          <span className="min-w-0 flex-1 truncate font-mono text-xs">{table.name}</span>
          <span className="numeric shrink-0 text-hint text-muted-foreground">
            {table.columns.length}
          </span>
        </button>
        <IconAction
          label={`Open the first rows of ${table.name} in a tab`}
          reveal
          className="mr-0.5 size-6 shrink-0 max-sm:size-8"
          onClick={() => onOpenTable(table)}
        >
          <Table />
        </IconAction>
      </div>
      {open && (
        <ul
          aria-label={`Columns of ${table.name}`}
          className="mb-1 ml-3 border-l border-hairline pl-2"
        >
          {table.columns.map((column) => {
            const info = types?.[column]
            const hit = needle !== "" && column.toLowerCase().includes(needle)
            return (
              <li key={column}>
                <button
                  type="button"
                  aria-label={`Write ${column} at the cursor`}
                  onClick={() => onInsert(column)}
                  className="flex h-6 w-full min-w-0 items-center gap-2 rounded-md px-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover max-sm:h-8"
                >
                  <span
                    className={cn(
                      "min-w-0 flex-1 truncate font-mono text-xs",
                      hit ? "text-foreground" : "text-muted-foreground",
                    )}
                  >
                    {column}
                  </span>
                  {info && (
                    <Tag
                      mono
                      title={`${info.type}${info.nullable ? "" : ", not null"}`}
                      className="block max-w-[50%] truncate"
                    >
                      {info.type}
                    </Tag>
                  )}
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </li>
  )
}

function SchemaPicker({
  schemas,
  current,
  onPick,
}: {
  schemas: CatalogHead["schemas"]
  current: string
  onPick: (schema: string) => void
}) {
  const { engine } = useDatabase()
  const [find, setFind] = useState("")
  const needle = find.trim().toLowerCase()
  const match = (name: string) => !needle || name.toLowerCase().includes(needle)
  const own = schemas.filter((schema) => !schema.system && match(schema.name))
  const system = schemas.filter((schema) => schema.system && match(schema.name))
  const item = (schema: CatalogHead["schemas"][number]) => (
    <DropdownMenuItem
      key={schema.name}
      className={cn(schema.name === current && "bg-accent")}
      onSelect={() => onPick(schema.name)}
    >
      <SchemaMark name={schema.name} />
      <span className="min-w-0 flex-1 truncate font-mono text-xs">{schema.name}</span>
      {schema.tables >= 0 && (
        <span className="numeric shrink-0 text-hint text-muted-foreground">
          {schema.tables.toLocaleString("en-US")}
        </span>
      )}
    </DropdownMenuItem>
  )
  return (
    <DropdownMenu onOpenChange={(open) => !open && setFind("")}>
      <DropdownMenuTrigger asChild>
        <Button
          size="sm"
          variant="ghost"
          aria-label={`${engine.nouns.container}: ${current || "default"}`}
          className="h-7 min-w-0 flex-1 justify-start gap-1.5 px-1.5"
        >
          <SchemaMark name={current} />
          <span className="min-w-0 truncate font-mono text-xs">{current || "default"}</span>
          <ChevronDown className="ml-auto size-3 text-muted-foreground" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64">
        {schemas.length > 12 && (
          <div className="p-1 pb-2">
            <Input
              value={find}
              placeholder={`Find a ${engine.nouns.container}`}
              aria-label={`Find a ${engine.nouns.container}`}
              className="h-7 sm:h-7"
              onChange={(event) => setFind(event.target.value)}
              // The menu reads typed letters as type-ahead; in the box they are text.
              onKeyDown={(event) => event.key !== "Escape" && event.stopPropagation()}
            />
          </div>
        )}
        <div className="max-h-72 overflow-y-auto">
          {own.map(item)}
          {system.length > 0 && (
            <>
              {own.length > 0 && <DropdownMenuSeparator />}
              <DropdownMenuLabel>The engine&rsquo;s own</DropdownMenuLabel>
              {system.map(item)}
            </>
          )}
          {own.length + system.length === 0 && (
            <p className="px-2 py-3 text-center text-hint text-muted-foreground">
              No {engine.nouns.container} is called that
            </p>
          )}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** A schema's own colour, stable for its name, as a small square before it. */
function SchemaMark({ name }: { name: string }) {
  return (
    <span
      aria-hidden
      className="size-2 shrink-0 rounded-sm"
      style={{ background: nameHue(name) }}
    />
  )
}

/* -------------------------------------------------------------- snippets */

function SnippetList({
  snippets,
  onOpen,
}: {
  snippets: Snippet[]
  onOpen: (sql: string, title?: string) => void
}) {
  const { engine } = useDatabase()
  const [find, setFind] = useState("")
  const needle = find.trim().toLowerCase()
  const list = needle
    ? snippets.filter(
        (snippet) =>
          snippet.title.toLowerCase().includes(needle) ||
          snippet.about.toLowerCase().includes(needle) ||
          snippet.sql.toLowerCase().includes(needle),
      )
    : snippets

  if (snippets.length === 0) {
    return (
      <EmptyState
        className="mt-2 border-0 px-3 py-8"
        title={`No snippets for ${engine.label}`}
        description="Nobody has written this engine's diagnostics down yet. The schema and the history are still here."
      />
    )
  }
  return (
    <>
      <RailSearch value={find} onChange={setFind} label="Find a snippet" />
      <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {list.length === 0 && <NothingMatches onClear={() => setFind("")} />}
        {SNIPPET_GROUPS.map((group) => {
          const inGroup = list.filter((snippet) => snippet.group === group)
          if (inGroup.length === 0) return null
          return (
            <section key={group} aria-label={group} className="pb-1">
              <h3 className="eyebrow flex items-center gap-1.5 px-2 pt-2 pb-1">
                {group}
                <span className="numeric">{inGroup.length}</span>
              </h3>
              <ul>
                {inGroup.map((snippet) => (
                  <li key={snippet.id}>
                    <button
                      type="button"
                      aria-label={`Open in a tab: ${snippet.title}`}
                      onClick={() => onOpen(snippet.sql, snippet.title)}
                      className="flex w-full min-w-0 flex-col gap-0.5 rounded-md px-2 py-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover"
                    >
                      <span className="truncate text-xs font-medium">{snippet.title}</span>
                      <span className="line-clamp-2 text-hint leading-snug text-muted-foreground">
                        {snippet.about}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )
        })}
      </div>
    </>
  )
}
