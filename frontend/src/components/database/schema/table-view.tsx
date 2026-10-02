"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import Link from "next/link"
import {
  ArrowRight,
  Backspace,
  Code,
  Copy,
  Fingerprint,
  GridSquare,
  Key,
  Linked,
  Notes,
  Pencil,
  Plus,
  SidebarLeftClose,
  SidebarLeftOpen,
  TextFormat,
  Trash,
} from "@/components/icons"
import { ApiError } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import type { PollState } from "@/hooks/use-poll"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { FormFact } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Metric } from "@/components/page"
import { EmptyNote, EmptyState } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
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
import { VerbActions, VerbMenu, type Verb } from "@/components/verbs"
import { rowObjectKind } from "@/components/database/data/kinds"
import { TableDefinition } from "@/components/database/data/structure"
import { grouped } from "@/components/database/data/view"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  objectParams,
  schemaParams,
  tableParams,
  type TableViewId,
} from "@/components/database/schema/address"
import {
  dropColumnRequest,
  dropConstraintRequest,
  dropForeignKeyRequest,
  dropIndexRequest,
  dropTableRequest,
  dropViewRequest,
  qualified,
  tableLimits,
  truncateRequest,
  viewQuery,
} from "@/components/database/schema/changes"
import { Facts } from "@/components/database/schema/facts"
import { GroupGlyph, groupSpec } from "@/components/database/schema/kinds"
import { ViewDialog, useViewReplace } from "@/components/database/schema/object-forms"
import { selectStatement } from "@/components/database/schema/select"
import { TableStatistics } from "@/components/database/schema/statistics"
import {
  AddColumnDialog,
  AddConstraintDialog,
  AddForeignKeyDialog,
  AddIndexDialog,
  CommentDialog,
  EditColumnDialog,
  RenameDialog,
} from "@/components/database/schema/table-forms"
import type {
  DbCatalog,
  DbColumn,
  DbTableDetail,
  SchemaObject,
} from "@/components/database/schema/types"
import { useDestroy } from "@/components/database/schema/use-destroy"

type Form =
  | { type: "add-column" }
  | { type: "edit-column"; column: DbColumn }
  | { type: "rename-column"; column: DbColumn }
  | { type: "comment-column"; column: DbColumn }
  | { type: "rename-table" }
  | { type: "comment-table" }
  | { type: "add-index" }
  | { type: "add-foreign-key" }
  | { type: "add-constraint" }
  | { type: "replace-view" }

const TOUCH = "max-sm:h-8"

/**
 * One table — or view — of the schema, read and changed.
 *
 * The head names it and carries what is done to the whole of it; under it,
 * what it amounts to in figures; then a strip of readings — its columns, its
 * indexes, its keys and constraints, the triggers on it, the statement that
 * makes it, its statistics — each with the one command that adds to it.
 *
 * A control is drawn only where the change can be made: the engine's forms
 * can write it (`ddlOperations`), the role may ask for it (`service.control`
 * to add and alter, `destructive` to drop and empty), and the connection is
 * not protected. What an engine's forms cannot do is said under the reading
 * it concerns, so nobody finds a limit by pressing a button.
 */
export function TableView({
  schema,
  name,
  view,
  onView,
  detail,
  catalog,
  asked,
  railOpen,
  onToggleRail,
  onChanged,
  onRenamed,
  onDropped,
}: {
  schema: string
  name: string
  view: TableViewId
  onView: (view: TableViewId) => void
  detail: PollState<DbTableDetail>
  /** The schema's catalogue, for the triggers on this table and where a new view may land. */
  catalog: DbCatalog | undefined
  /** Counts the times the reader asked for the schema to be read again. */
  asked: number
  railOpen: boolean
  onToggleRail: () => void
  /** The table's structure changed: what was read of it, and of the schema, is stale. */
  onChanged: () => void
  onRenamed: (to: string) => void
  onDropped: () => void
}) {
  const { id, engine, href, goto, readOnly } = useDatabase()
  const { can } = useAuth()
  const tabsId = useId()
  const tabs = useRef<HTMLDivElement>(null)
  const [form, setForm] = useState<Form | null>(null)
  const { destroy, dialog } = useDestroy()
  const data = detail.data
  const operations = engine.capabilities.ddlOperations
  const has = (operation: (typeof operations)[number]) => operations.includes(operation)

  const kind = rowObjectKind(data?.type)
  const isTable = kind === "table"
  const group =
    kind === "view"
      ? "views"
      : kind === "materialized view"
        ? "materializedViews"
        : kind === "dictionary"
          ? "dictionaries"
          : "tables"
  const info = catalog?.schemas.find((entry) => entry.name === (data?.schema ?? schema))
  // The engine's own namespaces are read, never changed; and an engine with
  // one schema to choose from changes only the one its names resolve to (an
  // attached file's tables are listed and left alone).
  const changeable =
    !info?.system &&
    (engine.can("schemas") || !catalog || (data?.schema ?? schema) === catalog.defaultSchema)
  const mayWrite = can("service.control") && !readOnly && changeable
  const mayDestroy = can("destructive") && !readOnly && changeable

  const triggers = useMemo(
    () =>
      ((catalog?.objects.triggers ?? []) as SchemaObject[]).filter(
        (trigger) => trigger.table === name && trigger.schema === (data?.schema ?? schema),
      ),
    [catalog, name, schema, data?.schema],
  )
  const replace = useViewReplace(schema, kind === "view" && mayWrite && has("views"))

  const primary = data?.indexes.find((index) => index.primary)
  const keyCount = data
    ? (data.primaryKey.length > 0 ? 1 : 0) + data.foreignKeys.length + data.constraints.length
    : 0
  const views: { id: TableViewId; label: string; count?: number }[] = [
    { id: "columns", label: "Columns", count: data?.columns.length },
    ...(isTable || (data?.indexes.length ?? 0) > 0
      ? [{ id: "indexes" as const, label: "Indexes", count: data?.indexes.length }]
      : []),
    ...(isTable
      ? [{ id: "keys" as const, label: "Keys & constraints", count: data ? keyCount : undefined }]
      : []),
    ...(engine.capabilities.catalogGroups.includes("triggers") && kind !== "dictionary"
      ? [
          {
            id: "triggers" as const,
            label: "Triggers",
            count: catalog ? triggers.length : undefined,
          },
        ]
      : []),
    { id: "definition", label: "Definition" },
    ...(isTable && (engine.can("tableStats") || engine.can("indexStats"))
      ? [{ id: "statistics" as const, label: "Statistics" }]
      : []),
  ]
  const open = views.some((entry) => entry.id === view) ? view : "columns"

  // The reading that is open is on screen in the strip, however narrow.
  useEffect(() => {
    tabs.current
      ?.querySelector<HTMLElement>(`[data-view="${open}"]`)
      ?.scrollIntoView({ block: "nearest", inline: "nearest" })
  }, [open])

  const onTabKey = (event: React.KeyboardEvent) => {
    const at = views.findIndex((entry) => entry.id === open)
    const to =
      event.key === "ArrowRight"
        ? (at + 1) % views.length
        : event.key === "ArrowLeft"
          ? (at - 1 + views.length) % views.length
          : event.key === "Home"
            ? 0
            : event.key === "End"
              ? views.length - 1
              : -1
    if (to < 0) return
    event.preventDefault()
    onView(views[to].id)
    tabs.current?.querySelector<HTMLElement>(`[data-view="${views[to].id}"]`)?.focus()
  }

  const full = qualified(data?.schema ?? schema, name)
  const word = groupSpec(group).label.toLowerCase()
  const subject = {
    name: full,
    facts: data && (
      <>
        {data.estimatedRows >= 0 && (
          <FormFact label="Rows">about {grouped(data.estimatedRows)}</FormFact>
        )}
        {data.size !== undefined && <FormFact label="Size">{bytes(data.size)}</FormFact>}
      </>
    ),
  }

  const openQuery = async () => {
    try {
      goto("query", { sql: await selectStatement(id, data?.schema ?? schema, name) })
    } catch (err) {
      notify.error(`Could not write a query for ${name}`, err)
    }
  }

  const tableVerbs: Verb[] = [
    { key: "copy", label: "Copy name", icon: Copy, run: () => void copyText(full, "Name copied") },
    ...(isTable && mayWrite && has("renameTable")
      ? [
          {
            key: "rename",
            label: "Rename",
            icon: TextFormat,
            run: () => setForm({ type: "rename-table" }),
          },
        ]
      : []),
    ...(isTable && mayWrite && has("tableComments")
      ? [
          {
            key: "comment",
            label: data?.comment ? "Edit comment" : "Add comment",
            icon: Notes,
            run: () => setForm({ type: "comment-table" }),
          },
        ]
      : []),
    ...(isTable && mayDestroy && has("truncate")
      ? [
          {
            key: "truncate",
            label: "Empty…",
            icon: Backspace,
            danger: true,
            group: "Destroy",
            run: () =>
              void destroy({
                request: truncateRequest(data?.schema ?? schema, name),
                title: "Empty table",
                subject,
                sentence:
                  "Every row of this table is removed. The table itself, its columns and its indexes stay.",
                done: `Emptied ${name}`,
                onDone: onChanged,
              }),
          },
        ]
      : []),
    ...(mayDestroy &&
    (isTable
      ? has("dropTable")
      : kind === "view"
        ? has("views")
        : kind === "materialized view" && has("materializedViews"))
      ? [
          {
            key: "drop",
            label: "Drop…",
            icon: Trash,
            danger: true,
            group: "Destroy",
            run: () =>
              void destroy({
                request: isTable
                  ? dropTableRequest(data?.schema ?? schema, name)
                  : dropViewRequest(data?.schema ?? schema, name, kind === "materialized view"),
                title: `Drop ${word}`,
                subject,
                sentence: isTable
                  ? "The table is removed with every row in it. Nothing here can bring it back."
                  : `The ${word} is removed. The tables it reads are untouched.`,
                done: `Dropped ${name}`,
                onDone: onDropped,
              }),
          },
        ]
      : []),
  ]

  const done = () => {
    onChanged()
  }

  const missing = !data && detail.error instanceof ApiError && detail.error.status === 404

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex min-h-10 shrink-0 items-center gap-1.5 border-b border-hairline bg-surface-header px-1.5">
        <IconAction
          label={railOpen ? "Hide the objects" : "Show the objects"}
          aria-pressed={railOpen}
          className={cn("size-7 shrink-0", TOUCH, "max-sm:w-8")}
          onClick={onToggleRail}
        >
          {railOpen ? <SidebarLeftClose /> : <SidebarLeftOpen />}
        </IconAction>
        <GroupGlyph group={group} />
        {/* Named whole: read part by part, its two halves are two words. */}
        <h2
          aria-label={full}
          title={full}
          className="flex min-w-0 items-baseline font-mono text-body font-medium"
        >
          {(data?.schema ?? schema) && (
            <span className="shrink-0 text-muted-foreground max-sm:hidden">
              {data?.schema ?? schema}.
            </span>
          )}
          <span className="min-w-[4ch] truncate">{name}</span>
        </h2>
        {data?.type && data.type !== "table" && <Tag className="shrink-0">{data.type}</Tag>}
        {!missing && (
          <div className="ml-auto flex shrink-0 items-center gap-1 pl-2">
            {engine.has("data") && (
              <Button size="xs" variant="outline" className={TOUCH} asChild>
                <Link
                  href={href("data", { schema: (data?.schema ?? schema) || null, table: name })}
                >
                  <GridSquare />
                  <span className="max-sm:sr-only">Open data</span>
                </Link>
              </Button>
            )}
            {engine.has("query") && (
              <Button
                size="xs"
                variant="outline"
                className={TOUCH}
                onClick={() => void openQuery()}
              >
                <Code />
                <span className="max-sm:sr-only">Query</span>
              </Button>
            )}
            <VerbMenu verbs={tableVerbs} label={`Actions for ${name}`} />
          </div>
        )}
      </div>

      {missing ? (
        <div className="flex min-h-0 flex-1 items-center justify-center p-6">
          <EmptyState
            className="border-0"
            title={`No table or view called ${name}`}
            description={`${schema || `This ${engine.nouns.container}`} has nothing of that name. It may have been renamed or dropped since the link was made.`}
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href={href("schema", schemaParams(schema))}>
                  Back to {schema || `the ${engine.nouns.container}`}
                </Link>
              </Button>
            }
          />
        </div>
      ) : !data && detail.error ? (
        <div className="p-4">
          <ReadFailed error={detail.error} onRetry={detail.refresh} />
        </div>
      ) : (
        <>
          {/* What the table amounts to, before any of its parts. */}
          <div className="shrink-0 space-y-3 border-b border-hairline px-4 py-3">
            {data ? (
              <div key="figures" className="animate-rise space-y-3">
                {data.comment ? (
                  <p className="max-w-3xl text-body text-muted-foreground">{data.comment}</p>
                ) : null}
                <Facts>
                  <Metric
                    label="Rows"
                    value={
                      data.estimatedRows >= 0 ? `~${grouped(data.estimatedRows)}` : "not counted"
                    }
                  />
                  {data.size !== undefined && <Metric label="Size" value={bytes(data.size)} />}
                  {data.dataSize !== undefined && data.indexSize !== undefined && (
                    <Metric
                      label="Data · indexes"
                      value={`${bytes(data.dataSize)} · ${bytes(data.indexSize)}`}
                    />
                  )}
                  {data.owner && <Metric label="Owner" value={data.owner} />}
                  {data.facts.map((fact) => (
                    <Metric key={fact.name} label={fact.name} value={fact.value} />
                  ))}
                </Facts>
              </div>
            ) : (
              <div aria-hidden className="flex gap-8">
                {["w-14", "w-12", "w-24", "w-16"].map((width) => (
                  <div key={width} className="space-y-2">
                    <Skeleton className="h-2.5 w-10" />
                    <Skeleton className={cn("h-3.5", width)} />
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* The strip lays itself out by its own width: one line with room,
              and on a phone the reading's commands take a line of their own
              under the readings rather than covering them. */}
          <div className="@container/strip shrink-0 border-b border-hairline px-1.5">
            <div className="flex min-h-10 items-stretch @max-[36rem]/strip:flex-wrap">
              <div
                ref={tabs}
                role="tablist"
                aria-label={`Readings of ${name}`}
                className="flex min-w-0 [scrollbar-width:none] items-stretch overflow-x-auto [&::-webkit-scrollbar]:hidden"
                onKeyDown={onTabKey}
              >
                {views.map((entry) => {
                  const selected = open === entry.id
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
                      className={cn(tabClasses(selected, "h-10"), "max-sm:px-2.5")}
                      onClick={() => onView(entry.id)}
                    >
                      {entry.label}
                      {entry.count !== undefined && <ChipCount>{entry.count}</ChipCount>}
                    </button>
                  )
                })}
              </div>
              {data && (
                <div className="ml-auto flex shrink-0 items-center gap-1 py-1 pl-2 empty:hidden @max-[36rem]/strip:basis-full @max-[36rem]/strip:justify-end @max-[36rem]/strip:pb-1.5">
                  {open === "columns" && isTable && mayWrite && has("addColumn") && (
                    <Button
                      size="xs"
                      className={TOUCH}
                      onClick={() => setForm({ type: "add-column" })}
                    >
                      <Plus />
                      Add column
                    </Button>
                  )}
                  {open === "indexes" && isTable && mayWrite && has("createIndex") && (
                    <Button
                      size="xs"
                      className={TOUCH}
                      onClick={() => setForm({ type: "add-index" })}
                    >
                      <Plus />
                      Add index
                    </Button>
                  )}
                  {open === "keys" && mayWrite && (
                    <>
                      {(has("uniqueConstraints") || has("checkConstraints")) && (
                        <Button
                          size="xs"
                          variant="outline"
                          aria-label="Add constraint"
                          className={TOUCH}
                          onClick={() => setForm({ type: "add-constraint" })}
                        >
                          <Plus />
                          {/* The strip is one line: the verb goes before the tabs do. */}
                          <span className="@max-[50rem]/strip:hidden">Add constraint</span>
                          <span className="@min-[50rem]/strip:hidden">Constraint</span>
                        </Button>
                      )}
                      {has("foreignKeys") && (
                        <Button
                          size="xs"
                          aria-label="Add foreign key"
                          className={TOUCH}
                          onClick={() => setForm({ type: "add-foreign-key" })}
                        >
                          <Plus />
                          <span className="@max-[50rem]/strip:hidden">Add foreign key</span>
                          <span className="@min-[50rem]/strip:hidden">Foreign key</span>
                        </Button>
                      )}
                    </>
                  )}
                  {open === "definition" && kind === "view" && mayWrite && has("views") && (
                    <Button
                      size="xs"
                      className={TOUCH}
                      disabled={replace?.supported === false}
                      onClick={() => setForm({ type: "replace-view" })}
                    >
                      <Pencil />
                      Replace the query
                    </Button>
                  )}
                </div>
              )}
            </div>
          </div>

          <div
            role="tabpanel"
            id={`${tabsId}-panel`}
            aria-labelledby={`${tabsId}-${open}`}
            className="flex min-h-0 min-w-0 flex-1 flex-col"
          >
            {!data ? (
              <TableSkeleton />
            ) : open === "columns" ? (
              <Columns
                detail={data}
                editable={isTable && mayWrite}
                droppable={isTable && mayDestroy && has("dropColumn")}
                limits={isTable && mayWrite ? tableLimits(operations, engine.label).columns : []}
                unchangeable={isTable && !changeable && can("service.control") && !readOnly}
                onForm={setForm}
                onDrop={(column) =>
                  void destroy({
                    request: dropColumnRequest(data.schema, data.name, column.name),
                    title: "Drop column",
                    subject: {
                      name: `${full}.${column.name}`,
                      facts: (
                        <>
                          <FormFact label="Type" mono>
                            {column.type}
                          </FormFact>
                          {data.estimatedRows >= 0 && (
                            <FormFact label="Rows">about {grouped(data.estimatedRows)}</FormFact>
                          )}
                        </>
                      ),
                    },
                    sentence:
                      "The column is removed with the value every row holds in it. Nothing here can bring them back.",
                    done: `Dropped ${column.name} from ${name}`,
                    onDone: onChanged,
                  })
                }
              />
            ) : open === "indexes" ? (
              <Indexes
                detail={data}
                droppable={isTable && mayDestroy && has("dropIndex")}
                limits={isTable && mayWrite ? tableLimits(operations, engine.label).indexes : []}
                onDrop={(index) =>
                  void destroy({
                    request: dropIndexRequest(data.schema, data.name, index.name),
                    title: "Drop index",
                    subject: {
                      name: index.name,
                      facts: (
                        <>
                          <FormFact label="On" mono>
                            {full}
                          </FormFact>
                          <FormFact label="Columns" mono>
                            {index.columns.join(", ")}
                          </FormFact>
                        </>
                      ),
                    },
                    sentence:
                      "The index is removed. Reads that used it scan the table until another index serves them; the rows are untouched.",
                    done: `Dropped ${index.name}`,
                    onDone: onChanged,
                  })
                }
              />
            ) : open === "keys" ? (
              <Keys
                detail={data}
                primaryConstraint={primary?.constraint}
                mayDrop={mayDestroy}
                limits={mayWrite ? tableLimits(operations, engine.label).keys : []}
                onDrop={(target) =>
                  void destroy({
                    request:
                      target.kind === "foreign key"
                        ? dropForeignKeyRequest(data.schema, data.name, target.name)
                        : dropConstraintRequest(
                            data.schema,
                            data.name,
                            target.name,
                            target.kind === "unique" || target.kind === "check"
                              ? target.kind
                              : undefined,
                          ),
                    title:
                      target.kind === "primary key" ? "Drop primary key" : `Drop ${target.kind}`,
                    subject: {
                      name: target.name,
                      facts: (
                        <FormFact label="On" mono>
                          {full}
                        </FormFact>
                      ),
                    },
                    sentence:
                      target.kind === "foreign key"
                        ? "Rows are no longer checked against the table it points at. The rows themselves stay."
                        : target.kind === "primary key"
                          ? "The table loses its key: its rows are no longer told apart by it. The rows themselves stay."
                          : "The rule is no longer enforced. The rows themselves stay.",
                    done: `Dropped ${target.name}`,
                    onDone: onChanged,
                  })
                }
              />
            ) : open === "triggers" ? (
              <Triggers triggers={triggers} read={catalog !== undefined} word={word} />
            ) : open === "definition" ? (
              <>
                {kind === "view" && replace?.supported === false && mayWrite && (
                  <p className="shrink-0 border-b border-hairline px-4 py-2 text-hint text-muted-foreground">
                    {replace.reason}
                  </p>
                )}
                <TableDefinition detail={data} />
              </>
            ) : (
              <TableStatistics detail={data} mayRun={!readOnly && changeable} asked={asked} />
            )}
          </div>
        </>
      )}

      {data && form?.type === "add-column" && (
        <AddColumnDialog detail={data} onClose={() => setForm(null)} onDone={done} />
      )}
      {data && form?.type === "edit-column" && (
        <EditColumnDialog
          detail={data}
          column={form.column}
          onClose={() => setForm(null)}
          onDone={done}
        />
      )}
      {data && form?.type === "rename-column" && (
        <RenameDialog
          detail={data}
          column={form.column.name}
          onClose={() => setForm(null)}
          onDone={done}
        />
      )}
      {data && form?.type === "comment-column" && (
        <CommentDialog
          detail={data}
          column={form.column}
          onClose={() => setForm(null)}
          onDone={done}
        />
      )}
      {data && form?.type === "rename-table" && (
        <RenameDialog
          detail={data}
          onClose={() => setForm(null)}
          onDone={() => undefined}
          onRenamed={onRenamed}
        />
      )}
      {data && form?.type === "comment-table" && (
        <CommentDialog detail={data} onClose={() => setForm(null)} onDone={done} />
      )}
      {data && form?.type === "add-index" && (
        <AddIndexDialog detail={data} onClose={() => setForm(null)} onDone={done} />
      )}
      {data && form?.type === "add-foreign-key" && (
        <AddForeignKeyDialog detail={data} onClose={() => setForm(null)} onDone={done} />
      )}
      {data && form?.type === "add-constraint" && (
        <AddConstraintDialog detail={data} onClose={() => setForm(null)} onDone={done} />
      )}
      {data && form?.type === "replace-view" && (
        <ViewDialog
          schemas={catalog?.schemas ?? []}
          schema={data.schema}
          existing={{
            schema: data.schema,
            name: data.name,
            query: viewQuery(data.createSql ?? "") ?? "",
          }}
          onClose={() => setForm(null)}
          onDone={done}
        />
      )}
      {dialog}
    </div>
  )
}

function TableSkeleton() {
  // The coming silhouette: a header line and rows of a name, a type and a mark.
  return (
    <div aria-hidden className="space-y-3.5 p-4">
      <Skeleton className="h-2.5 w-64 max-w-full" />
      {["w-24", "w-32", "w-20", "w-28", "w-36", "w-24"].map((width, index) => (
        <div key={index} className="flex items-center gap-6">
          <Skeleton className={cn("h-3.5", width)} />
          <Skeleton className="h-3.5 w-20" />
          <Skeleton className="ml-auto h-3.5 w-12" />
        </div>
      ))}
    </div>
  )
}

/** A quiet line under a reading: what the engine's forms cannot do to it. */
function Limits({ lines }: { lines: string[] }) {
  if (lines.length === 0) return null
  return (
    <div className="space-y-1 px-4 py-3 text-hint leading-relaxed text-muted-foreground">
      {lines.map((line) => (
        <p key={line}>{line}</p>
      ))}
    </div>
  )
}

/** Below this the columns of a table are read down, a column to a block, instead of across. */
const NARROW = 640
const HEAD = "h-8 px-3 first:pl-4 last:pr-4"
const CELL = "px-3 py-1 first:pl-4 last:pr-4"
/** A row's controls at the row's own height, so a line of the table is a line of text. */
const ROW_ACTIONS = "justify-end [&_button]:size-6"

function Columns({
  detail,
  editable,
  droppable,
  limits,
  unchangeable,
  onForm,
  onDrop,
}: {
  detail: DbTableDetail
  /** The role may add and alter here; which of the forms exist is the engine's. */
  editable: boolean
  droppable: boolean
  limits: string[]
  /** The table is in a schema the forms leave alone, and that is why nothing can be pressed. */
  unchangeable: boolean
  onForm: (form: Form) => void
  onDrop: (column: DbColumn) => void
}) {
  const { engine, href } = useDatabase()
  const operations = engine.capabilities.ddlOperations
  // Where the engine's key does not tell rows apart it is the order they are
  // kept in, and is called that.
  const keyWord = engine.capabilities.rowIdentity === "none" ? "sort key" : "primary"
  const foreign = new Map(
    detail.foreignKeys.flatMap((key) =>
      key.columns.map((column, index) => [column, { key, index }] as const),
    ),
  )
  const unique = new Set(
    detail.indexes
      .filter(
        (index) => index.unique && !index.primary && index.columns.length === 1 && !index.predicate,
      )
      .map((index) => index.columns[0]),
  )
  const verbsFor = (column: DbColumn): Verb[] => [
    ...(editable && operations.includes("alterColumn")
      ? [
          {
            key: "edit",
            label: `Edit ${column.name}`,
            icon: Pencil,
            inline: true,
            run: () => onForm({ type: "edit-column", column }),
          },
        ]
      : []),
    ...(editable && operations.includes("renameColumn")
      ? [
          {
            key: "rename",
            label: "Rename",
            icon: TextFormat,
            run: () => onForm({ type: "rename-column", column }),
          },
        ]
      : []),
    ...(editable && operations.includes("columnComments")
      ? [
          {
            key: "comment",
            label: column.comment ? "Edit comment" : "Add comment",
            icon: Notes,
            run: () => onForm({ type: "comment-column", column }),
          },
        ]
      : []),
    ...(droppable
      ? [{ key: "drop", label: "Drop…", icon: Trash, danger: true, run: () => onDrop(column) }]
      : []),
  ]
  const acts = editable || droppable
  // The readings are drawn across while there is room for them and down when
  // there is not: one shape, chosen by the width the table is given.
  const [box, width] = useColumnWidth<HTMLDivElement>()
  const narrow = width > 0 && width < NARROW

  const marks = (column: DbColumn) => {
    const reference = foreign.get(column.name)
    return (
      <>
        {detail.primaryKey.includes(column.name) && (
          <span className="flex items-center gap-1">
            <Key aria-hidden className="size-3 text-chart-2" />
            <Tag>{keyWord}</Tag>
          </span>
        )}
        {unique.has(column.name) && (
          <span className="flex items-center gap-1">
            <Fingerprint aria-hidden className="size-3 text-muted-foreground" />
            <Tag>unique</Tag>
          </span>
        )}
        {column.identity && <Tag title={column.identity}>numbered</Tag>}
        {column.generated && <Tag>computed</Tag>}
        {reference && (
          <Link
            href={href(
              "schema",
              tableParams(reference.key.refSchema ?? detail.schema, reference.key.refTable),
            )}
            className="flex items-center gap-1 rounded-sm font-mono text-xs text-muted-foreground focus-ring transition-colors hover:text-foreground"
          >
            <Linked aria-hidden className="size-3 text-chart-1" />
            {reference.key.refTable}.{reference.key.refColumns[reference.index]}
          </Link>
        )}
      </>
    )
  }
  const said = (column: DbColumn) =>
    column.generated ? `= ${column.generated}` : (column.default ?? "")

  return (
    <div ref={box} className="@container min-h-0 flex-1 overflow-y-auto">
      {narrow ? (
        <ul aria-label="Columns" className="divide-y divide-hairline border-b border-hairline">
          {detail.columns.map((column) => {
            const verbs = verbsFor(column)
            return (
              <li key={column.name} className="group flex items-start gap-2 py-2 pr-2 pl-4">
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="font-mono text-xs font-medium">{column.name}</span>
                    <Tag mono className="block max-w-full truncate" title={column.type}>
                      {column.type}
                    </Tag>
                  </div>
                  <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-muted-foreground">
                    <span>{column.nullable ? "takes NULL" : "not null"}</span>
                    {said(column) && (
                      <span className="max-w-full min-w-0 truncate font-mono" title={said(column)}>
                        {column.generated ? "" : "default "}
                        {said(column)}
                      </span>
                    )}
                    {marks(column)}
                  </div>
                  {column.comment && (
                    <p className="text-hint text-muted-foreground">{column.comment}</p>
                  )}
                </div>
                {verbs.length > 0 && (
                  <VerbActions verbs={verbs} menuLabel={`Actions for ${column.name}`} />
                )}
              </li>
            )
          })}
        </ul>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className={cn(HEAD, "w-10 pr-0 text-right @max-[52rem]:hidden")}>
                #
              </TableHead>
              <TableHead className={HEAD}>Name</TableHead>
              <TableHead className={HEAD}>Type</TableHead>
              <TableHead className={HEAD}>NULL</TableHead>
              <TableHead className={HEAD}>Default</TableHead>
              <TableHead className={HEAD}>Key</TableHead>
              {acts && (
                <TableHead className={cn(HEAD, "w-px")}>
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {detail.columns.map((column) => {
              const verbs = verbsFor(column)
              return (
                <TableRow key={column.name} className="group">
                  <TableCell
                    className={cn(
                      CELL,
                      "numeric pr-0 text-right text-muted-foreground @max-[52rem]:hidden",
                    )}
                  >
                    {column.position}
                  </TableCell>
                  <TableCell className={CELL}>
                    <span className="font-mono font-medium">{column.name}</span>
                    {column.comment && (
                      <p
                        className="max-w-80 truncate text-hint text-muted-foreground"
                        title={column.comment}
                      >
                        {column.comment}
                      </p>
                    )}
                  </TableCell>
                  <TableCell className={CELL}>
                    <Tag mono className="block max-w-48 truncate" title={column.type}>
                      {column.type}
                    </Tag>
                  </TableCell>
                  <TableCell className={cn(CELL, "text-muted-foreground")}>
                    {column.nullable ? "yes" : "no"}
                  </TableCell>
                  <TableCell
                    className={cn(CELL, "max-w-40 truncate font-mono text-muted-foreground")}
                    title={said(column)}
                  >
                    {said(column)}
                  </TableCell>
                  <TableCell className={CELL}>
                    <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                      {marks(column)}
                    </span>
                  </TableCell>
                  {acts && (
                    <TableCell className={cn(CELL, "pl-0")}>
                      {verbs.length > 0 && (
                        <VerbActions
                          verbs={verbs}
                          dim
                          menuLabel={`Actions for ${column.name}`}
                          className={ROW_ACTIONS}
                        />
                      )}
                    </TableCell>
                  )}
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
      <Limits
        lines={
          unchangeable
            ? [
                `Changes are made in ${engine.label}'s own ${engine.nouns.container} only: this one is read here and left as it is.`,
              ]
            : limits
        }
      />
    </div>
  )
}

function Indexes({
  detail,
  droppable,
  limits,
  onDrop,
}: {
  detail: DbTableDetail
  droppable: boolean
  limits: string[]
  onDrop: (index: DbTableDetail["indexes"][number]) => void
}) {
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      {detail.indexes.length === 0 ? (
        <EmptyNote className="px-4 py-10">
          No index. Every read of this {rowObjectKind(detail.type) === "table" ? "table" : "view"}{" "}
          scans it.
        </EmptyNote>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className={HEAD}>Name</TableHead>
              <TableHead className={HEAD}>Columns</TableHead>
              <TableHead className={HEAD}>Kind</TableHead>
              <TableHead className={cn(HEAD, "max-md:hidden")}>Method</TableHead>
              <TableHead className={cn(HEAD, "text-right max-md:hidden")}>Size</TableHead>
              {droppable && (
                <TableHead className={cn(HEAD, "w-px")}>
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {detail.indexes.map((index) => (
              <TableRow key={index.name} className="group">
                <TableCell className={cn(CELL, "font-mono font-medium")}>{index.name}</TableCell>
                <TableCell className={cn(CELL, "max-w-96 font-mono")}>
                  <span className="block truncate" title={index.definition}>
                    {index.columns.join(", ")}
                    {index.include && index.include.length > 0 && (
                      <span className="text-muted-foreground">
                        {" "}
                        include {index.include.join(", ")}
                      </span>
                    )}
                    {index.predicate && (
                      <span className="text-muted-foreground"> where {index.predicate}</span>
                    )}
                  </span>
                </TableCell>
                <TableCell className={CELL}>
                  <span className="flex items-center gap-2">
                    {index.primary ? <Tag>primary</Tag> : index.unique ? <Tag>unique</Tag> : null}
                    {index.invalid && <Tag tone="warning">not usable</Tag>}
                  </span>
                </TableCell>
                <TableCell className={cn(CELL, "text-muted-foreground max-md:hidden")}>
                  {index.method?.toLowerCase()}
                </TableCell>
                <TableCell
                  className={cn(CELL, "numeric text-right text-muted-foreground max-md:hidden")}
                >
                  {index.size !== undefined ? bytes(index.size) : ""}
                </TableCell>
                {droppable && (
                  <TableCell className={cn(CELL, "pl-0")}>
                    {/* An index that enforces a constraint goes with it, under
                        Keys & constraints; it cannot be dropped as an index. */}
                    {!index.constraint && (
                      <VerbActions
                        dim
                        className={ROW_ACTIONS}
                        verbs={[
                          {
                            key: "drop",
                            label: `Drop ${index.name}`,
                            icon: Trash,
                            inline: true,
                            run: () => onDrop(index),
                          },
                        ]}
                      />
                    )}
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {droppable && detail.indexes.some((index) => index.constraint) && (
        <p className="px-4 pt-3 text-hint text-muted-foreground">
          An index that enforces a key or a constraint is dropped with it, under Keys &amp;
          constraints.
        </p>
      )}
      <Limits lines={limits} />
    </div>
  )
}

type KeyKind = "primary key" | "foreign key" | "unique" | "check" | "exclusion"

function Keys({
  detail,
  primaryConstraint,
  mayDrop,
  limits,
  onDrop,
}: {
  detail: DbTableDetail
  /** The name the primary key is dropped by, where the engine gives it one. */
  primaryConstraint: string | undefined
  mayDrop: boolean
  limits: string[]
  onDrop: (target: { kind: KeyKind; name: string }) => void
}) {
  const { engine, href } = useDatabase()
  const operations = engine.capabilities.ddlOperations
  const drops: Record<KeyKind, boolean> = {
    "primary key":
      mayDrop && operations.includes("uniqueConstraints") && Boolean(primaryConstraint),
    "foreign key": mayDrop && operations.includes("foreignKeys"),
    unique: mayDrop && operations.includes("uniqueConstraints"),
    check: mayDrop && operations.includes("checkConstraints"),
    exclusion: mayDrop && operations.includes("uniqueConstraints"),
  }
  const acts = Object.values(drops).some(Boolean)
  const empty =
    detail.primaryKey.length === 0 &&
    detail.foreignKeys.length === 0 &&
    detail.constraints.length === 0
  const drop = (kind: KeyKind, name: string) =>
    drops[kind] &&
    name !== "" && (
      <VerbActions
        dim
        className={ROW_ACTIONS}
        verbs={[
          {
            key: "drop",
            label: `Drop ${name}`,
            icon: Trash,
            inline: true,
            run: () => onDrop({ kind, name }),
          },
        ]}
      />
    )

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      {empty ? (
        <EmptyNote className="px-4 py-10">
          No key and no constraint: nothing tells this table&rsquo;s rows apart, and nothing checks
          them.
        </EmptyNote>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className={HEAD}>Name</TableHead>
              <TableHead className={HEAD}>Kind</TableHead>
              <TableHead className={HEAD}>Definition</TableHead>
              {acts && (
                <TableHead className={cn(HEAD, "w-px")}>
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {detail.primaryKey.length > 0 && (
              <TableRow className="group">
                <TableCell className={cn(CELL, "font-mono font-medium")}>
                  {primaryConstraint ?? <span className="text-muted-foreground">unnamed</span>}
                </TableCell>
                <TableCell className={CELL}>
                  <span className="flex items-center gap-1">
                    <Key aria-hidden className="size-3 text-chart-2" />
                    <Tag>
                      {engine.capabilities.rowIdentity === "none" ? "sorting key" : "primary key"}
                    </Tag>
                  </span>
                </TableCell>
                <TableCell className={cn(CELL, "font-mono")}>
                  ({detail.primaryKey.join(", ")})
                </TableCell>
                {acts && (
                  <TableCell className={cn(CELL, "pl-0")}>
                    {drop("primary key", primaryConstraint ?? "")}
                  </TableCell>
                )}
              </TableRow>
            )}
            {detail.foreignKeys.map((key) => (
              <TableRow key={`fk:${key.name}`} className="group">
                <TableCell className={cn(CELL, "font-mono font-medium")}>{key.name}</TableCell>
                <TableCell className={CELL}>
                  <span className="flex items-center gap-1">
                    <Linked aria-hidden className="size-3 text-chart-1" />
                    <Tag>foreign key</Tag>
                  </span>
                </TableCell>
                <TableCell className={cn(CELL, "font-mono")}>
                  <span className="flex flex-wrap items-center gap-x-1.5">
                    ({key.columns.join(", ")})
                    <ArrowRight aria-hidden className="size-3 text-muted-foreground" />
                    <Link
                      href={href(
                        "schema",
                        tableParams(key.refSchema ?? detail.schema, key.refTable),
                      )}
                      className="rounded-sm underline-offset-2 focus-ring hover:underline"
                    >
                      {key.refSchema && key.refSchema !== detail.schema
                        ? `${key.refSchema}.${key.refTable}`
                        : key.refTable}
                    </Link>
                    ({key.refColumns.join(", ")})
                    {key.onDelete && key.onDelete !== "NO ACTION" && (
                      <span className="font-sans text-muted-foreground">
                        on delete {key.onDelete.toLowerCase()}
                      </span>
                    )}
                    {key.onUpdate && key.onUpdate !== "NO ACTION" && (
                      <span className="font-sans text-muted-foreground">
                        on update {key.onUpdate.toLowerCase()}
                      </span>
                    )}
                  </span>
                </TableCell>
                {acts && (
                  <TableCell className={cn(CELL, "pl-0")}>
                    {drop("foreign key", key.name)}
                  </TableCell>
                )}
              </TableRow>
            ))}
            {detail.constraints.map((constraint, index) => (
              <TableRow key={`c:${index}:${constraint.name}`} className="group">
                <TableCell className={cn(CELL, "font-mono font-medium")}>
                  {constraint.name || <span className="text-muted-foreground">unnamed</span>}
                </TableCell>
                <TableCell className={CELL}>
                  <Tag>{constraint.type}</Tag>
                </TableCell>
                <TableCell className={cn(CELL, "max-w-[40rem] font-mono")}>
                  <span className="block truncate" title={constraint.definition}>
                    {constraint.definition ?? `(${constraint.columns.join(", ")})`}
                  </span>
                </TableCell>
                {acts && (
                  <TableCell className={cn(CELL, "pl-0")}>
                    {drop(constraint.type, constraint.name)}
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {detail.referencedBy.length > 0 && (
        <section aria-label="Referenced by" className="pt-4">
          <h3 className="flex items-baseline gap-2 px-4 pb-1 text-title font-medium">
            Referenced by
            <span className="numeric text-xs font-normal text-muted-foreground">
              {detail.referencedBy.length}
            </span>
          </h3>
          <ul className="divide-y divide-hairline border-y border-hairline">
            {detail.referencedBy.map((reference) => (
              <li key={`${reference.schema}.${reference.table}.${reference.name}`}>
                <Link
                  href={href("schema", {
                    ...tableParams(reference.schema ?? detail.schema, reference.table),
                    view: "keys",
                  })}
                  className="flex min-h-8 items-center gap-2 px-4 py-1.5 focus-ring-inset transition-colors hover:bg-row-hover"
                >
                  <GroupGlyph group="tables" />
                  <span className="font-mono text-xs font-medium">
                    {reference.schema && reference.schema !== detail.schema
                      ? `${reference.schema}.${reference.table}`
                      : reference.table}
                  </span>
                  <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">
                    ({reference.columns.join(", ")}) → ({reference.refColumns.join(", ")})
                  </span>
                  {reference.onDelete && reference.onDelete !== "NO ACTION" && (
                    <span className="shrink-0 text-xs text-muted-foreground max-sm:hidden">
                      on delete {reference.onDelete.toLowerCase()}
                    </span>
                  )}
                  <ArrowRight
                    aria-hidden
                    className="ml-auto size-3 shrink-0 text-muted-foreground"
                  />
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
      <Limits lines={limits} />
    </div>
  )
}

function Triggers({
  triggers,
  read,
  word,
}: {
  triggers: SchemaObject[]
  /** The catalogue that lists them has been read. */
  read: boolean
  word: string
}) {
  const { href } = useDatabase()
  if (!read) return <TableSkeleton />
  if (triggers.length === 0) {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto">
        <EmptyNote className="px-4 py-10">No trigger fires on this {word}.</EmptyNote>
      </div>
    )
  }
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <ul className="divide-y divide-hairline border-b border-hairline">
        {triggers.map((trigger) => (
          <li key={trigger.name}>
            <Link
              href={href("schema", objectParams(trigger))}
              className="flex min-h-9 items-center gap-2 px-4 py-1.5 focus-ring-inset transition-colors hover:bg-row-hover"
            >
              <GroupGlyph group="triggers" />
              <span className="font-mono text-xs font-medium">{trigger.name}</span>
              {trigger.detail && (
                <span className="min-w-0 truncate text-xs text-muted-foreground">
                  {trigger.detail}
                </span>
              )}
              <ArrowRight aria-hidden className="ml-auto size-3 shrink-0 text-muted-foreground" />
            </Link>
          </li>
        ))}
      </ul>
    </div>
  )
}
