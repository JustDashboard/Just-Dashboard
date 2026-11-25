"use client"

import { useEffect, useMemo, useRef, useState } from "react"
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
import { grouped, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { PollState } from "@/hooks/use-poll"
import { IconAction, rowReveal } from "@/components/icon-action"
import { EmptyNote, EmptyState, ErrorState } from "@/components/state"
import { Stale } from "@/components/stale"
import { StatusDot } from "@/components/status-dot"
import { ViewTabs } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { TabsContent } from "@/components/ui/tabs"
import { VerbMenu, type Verb } from "@/components/verbs"
import { KindGlyph, rowObjectKind } from "@/components/database/data/kinds"
import { ContainerPicker } from "@/components/database/kit/container-picker"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  RAIL_ROW,
  RailFind,
  RailGroupHead,
  RailNoMatch,
  RailSkeleton,
  RailTitle,
} from "@/components/database/workbench/rail"
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
  return (
    <div data-slot="query-rail" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <ViewTabs
        label="Saved queries, history, schema and snippets"
        views={VIEWS.map((entry) => ({
          key: entry.id,
          label: entry.label,
          count: entry.id === "saved" ? saved.data?.length : undefined,
        }))}
        value={view}
        onValueChange={onView}
        rootClassName="min-h-0 flex-1"
        className="bg-surface-header [&_[role=tab]]:px-1.5"
      >
        <TabsContent value={view} className="flex min-h-0 flex-col">
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
        </TabsContent>
      </ViewTabs>
    </div>
  )
}

/* ---------------------------------------------------------------- shared */

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
      <ErrorState retryable error={saved.error} onRetry={saved.refresh} className="m-2" />
    ) : (
      <RailSkeleton lines={2} groups={0} />
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
      <RailFind value={find} onChange={setFind} placeholder="Find a saved query" />
      <ul ref={rows} aria-label="Saved queries" className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {list.length === 0 && <RailNoMatch as="li" onClear={() => setFind("")} />}
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
      <ErrorState retryable error={history.error} onRetry={history.refresh} className="m-2" />
    ) : (
      <RailSkeleton lines={2} groups={0} />
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
      <RailFind
        value={find}
        onChange={setFind}
        placeholder="Find in the history"
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
        {list.length === 0 && <RailNoMatch as="li" onClear={() => setFind("")} />}
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
                      ? `${grouped(entry.rowsAffected!)} changed`
                      : `${grouped(entry.rowCount)} ${entry.rowCount === 1 ? "row" : "rows"}`}
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
      <div className="flex shrink-0 items-center gap-1 border-b border-hairline p-1">
        {engine.can("schemas") && schemas.length > 1 ? (
          <ContainerPicker
            items={schemas.map((entry) => ({
              name: entry.name,
              system: entry.system,
              figure: entry.tables >= 0 ? grouped(entry.tables) : undefined,
            }))}
            current={shown}
            noun={engine.nouns.container}
            nouns={engine.nouns.containers}
            onPick={onPick}
          />
        ) : (
          <RailTitle className="font-mono text-xs font-normal text-muted-foreground">
            {shown || engine.nouns.objects}
          </RailTitle>
        )}
        <IconAction
          label={`Read the ${engine.nouns.objects} again`}
          className="size-7 shrink-0"
          onClick={schema.refresh}
        >
          <RefreshClockwise />
        </IconAction>
      </div>
      {/* The tree is the last reading: the read just asked for failed. */}
      <Stale
        form="strip"
        error={model.tables.length > 0 ? schema.error : undefined}
        subject={`the ${engine.nouns.objects}`}
        onRetry={schema.refresh}
      />
      <RailFind
        value={find}
        onChange={setFind}
        placeholder={`Find a ${engine.nouns.object} or a column`}
      />
      <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {schema.error && model.tables.length === 0 ? (
          <ErrorState retryable error={schema.error} onRetry={schema.refresh} className="m-1" />
        ) : schema.loading && model.tables.length === 0 ? (
          <RailSkeleton groups={0} />
        ) : tables.length === 0 ? (
          needle ? (
            <RailNoMatch onClear={() => setFind("")} />
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
            Show {Math.min(STEP, tables.length - limit)} more of {grouped(tables.length)}
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
      <div
        className={cn(
          "group flex items-center rounded-md transition-colors hover:bg-row-hover",
          RAIL_ROW,
        )}
      >
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
      <RailFind value={find} onChange={setFind} placeholder="Find a snippet" />
      <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {list.length === 0 && <RailNoMatch as="li" onClear={() => setFind("")} />}
        {SNIPPET_GROUPS.map((group) => {
          const inGroup = list.filter((snippet) => snippet.group === group)
          if (inGroup.length === 0) return null
          return (
            <section key={group} aria-label={group} className="pt-1.5">
              <RailGroupHead label={group} count={inGroup.length} />
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
