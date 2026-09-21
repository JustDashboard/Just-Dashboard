"use client"

import { useMemo, useState } from "react"
import {
  AcronymJson,
  CloudUpload,
  CodeBracket,
  Download,
  Layout,
  MoreHorizontal,
  Play,
  Plus,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, downloadUrl, get, patch, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { cn, ringSafeScroll } from "@/lib/utils"
import type { DbConnection, DbTable, MongoCollectionInfo, QueryResult } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Pane, PaneFooter, PaneHeader, Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ResultGrid } from "@/components/database/result-grid"
import { ImportDialog } from "@/components/database/import-dialog"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { FormNote } from "@/components/form"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]
const PAGE = 100
/**
 * How many documents an export writes before it stops. The server caps it too;
 * this is the number the menu promises, sent as the request's own limit so the
 * sentence and the file cannot drift apart.
 */
const EXPORT_CAP = 100_000

/** The three readings of one collection, in the order the strip draws them. */
const VIEWS = [
  { key: "documents", label: "Documents" },
  { key: "indexes", label: "Indexes" },
  { key: "aggregate", label: "Aggregate" },
] as const

/**
 * MongoDB, in its own vocabulary.
 *
 * It shares the grid with the SQL engines — a list of documents still reads
 * best as a table, and the union-of-keys column set is what makes an evolving
 * schema legible — but nothing else. A filter is a document, not a WHERE
 * clause; the query surface is an aggregation pipeline, not SQL; and an edit is
 * a whole-document replace, so the editor is JSON rather than a field form.
 *
 * The _id shown in the grid is bare hex, which is what makes "edit the row I am
 * looking at" work: the value on screen is the value that goes back as the
 * filter.
 */
export function MongoBrowser({ conn, confirm }: { conn: DbConnection; confirm: ConfirmFn }) {
  const { can } = useAuth()
  const [tab, setTab] = useViewState("db.mongo.tab", "documents")
  const [database, setDatabase] = useSessionState(
    `databases.${conn.id}.mongo.database`,
    conn.database,
  )
  const [collection, setCollection] = useSessionState<string | undefined>(
    `databases.${conn.id}.mongo.collection`,
    undefined,
  )
  const [filter, setFilter] = useSessionState(`databases.${conn.id}.mongo.filter`, "{}")
  const [applied, setApplied] = useSessionState(`databases.${conn.id}.mongo.applied`, "{}")
  const [skip, setSkip] = useSessionState(`databases.${conn.id}.mongo.skip`, 0)
  const [editing, setEditing] = useState<{ doc: string; id: unknown } | null>(null)
  const [inserting, setInserting] = useState(false)
  const [importing, setImporting] = useState(false)

  const databases = usePoll(
    (signal) =>
      get<{ name: string; size: number }[]>(`/databases/${conn.id}/schemas`, undefined, signal),
    0,
    [conn.id],
  )
  const dbName = database || databases.data?.[0]?.name || ""

  const collections = usePoll(
    (signal) =>
      dbName
        ? get<DbTable[]>(`/databases/${conn.id}/tables`, { schema: dbName }, signal)
        : Promise.resolve([] as DbTable[]),
    0,
    [conn.id, dbName],
  )
  const info = usePoll(
    (signal) =>
      collection
        ? get<MongoCollectionInfo>(
            `/databases/${conn.id}/collections/indexes`,
            { schema: dbName, table: collection },
            signal,
          )
        : Promise.resolve(null as unknown as MongoCollectionInfo),
    0,
    [conn.id, dbName, collection],
  )
  const docs = usePoll(
    (signal) =>
      collection
        ? get<QueryResult>(
            `/databases/${conn.id}/browse`,
            { schema: dbName, table: collection, filter: applied, limit: PAGE, offset: skip },
            signal,
          )
        : Promise.resolve(null as unknown as QueryResult),
    0,
    [conn.id, dbName, collection, applied, skip],
  )

  const canWrite = can("service.control")
  const reload = () => {
    docs.refresh()
    collections.refresh()
  }

  const runFilter = () => {
    setSkip(0)
    setApplied(filter.trim() || "{}")
  }

  const idFilter = (row: Record<string, unknown>) => JSON.stringify({ _id: row._id })

  // Export honours the filter currently applied, so what downloads is what is
  // on screen rather than the whole collection — which is almost never what
  // somebody looking at a filtered view meant.
  const exportDocs = (format: "csv" | "json") => {
    if (!collection) return
    const a = document.createElement("a")
    a.href = downloadUrl(`/databases/${conn.id}/export`, {
      schema: dbName,
      table: collection,
      filter: applied,
      format,
      limit: EXPORT_CAP,
    })
    a.click()
  }

  const editDoc = (row: Record<string, unknown>) =>
    setEditing({ doc: JSON.stringify(row, null, 2), id: row._id })

  const saveDoc = async (json: string, id: unknown) => {
    await patch(`/databases/${conn.id}/documents`, {
      database: dbName,
      collection,
      filter: JSON.stringify({ _id: id }),
      document: json,
    })
    notify.success("Document saved")
    setEditing(null)
    reload()
  }

  const insertDoc = async (json: string) => {
    await post(`/databases/${conn.id}/documents`, {
      database: dbName,
      collection,
      document: json,
    })
    notify.success("Document inserted")
    setInserting(false)
    reload()
  }

  const deleteDoc = (row: Record<string, unknown>) =>
    confirm({
      title: "Delete document",
      confirmLabel: "Delete",
      description: (
        <p>
          Permanently removes the document with <span className="font-mono text-xs">_id</span>{" "}
          <span className="font-mono text-xs">{String(row._id)}</span> from <b>{collection}</b>.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/documents`, {
          body: { database: dbName, collection, filter: idFilter(row) },
          confirm: c,
        })
        notify.success("Document deleted")
        reload()
      },
    })

  const dropCollection = () =>
    confirm({
      title: "Drop collection",
      phrase: collection,
      confirmLabel: "Drop",
      description: (
        <p>
          Permanently destroys <b>{collection}</b> and every document in it.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/collections`, {
          body: { database: dbName, collection },
          confirm: c,
        })
        notify.success(`Dropped ${collection}`)
        setCollection(undefined)
        collections.refresh()
      },
    })

  return (
    // One workbench sized to the window, the shape the SQL browser has: a
    // collection rail, a hairline, and the documents beside it. It was two
    // framed cards and a filled tab list on a page that scrolled.
    <Pane className="min-h-0 flex-1">
      <div className="grid min-h-0 flex-1 grid-rows-[minmax(0,13rem)_minmax(0,1fr)] lg:grid-cols-[16rem_minmax(0,1fr)] lg:grid-rows-1">
        <div className="flex min-h-0 min-w-0 flex-col border-b border-hairline lg:border-r lg:border-b-0">
          {databases.data && databases.data.length > 1 && (
            <div className="shrink-0 border-b border-hairline p-2.5">
              <Select
                value={dbName}
                onValueChange={(v) => {
                  setDatabase(v)
                  setCollection(undefined)
                  setSkip(0)
                }}
              >
                <SelectTrigger size="sm" className="h-7 w-full text-xs sm:h-7" aria-label="Database">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {databases.data.map((d) => (
                    <SelectItem key={d.name} value={d.name}>
                      {d.name} {d.size > 0 && `(${bytes(d.size)})`}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          <div className={cn("min-h-0 flex-1 space-y-px overflow-y-auto p-1", ringSafeScroll)}>
            {collections.loading && <LoadingRows rows={4} />}
            {collections.data?.map((c) => (
              <button
                key={c.name}
                type="button"
                aria-pressed={collection === c.name}
                onClick={() => {
                  setCollection(c.name)
                  setSkip(0)
                  setFilter("{}")
                  setApplied("{}")
                }}
                className={cn(
                  "flex w-full min-w-0 flex-col rounded-md px-2 py-1.5 text-left focus-ring-inset transition-colors",
                  collection === c.name
                    ? "bg-accent font-medium text-foreground"
                    : "hover:bg-row-hover",
                )}
              >
                <span className="truncate text-body">{c.name}</span>
                <span className="truncate text-hint text-muted-foreground">
                  {plural(c.estimatedRows, "document")}
                  {c.size ? ` · ${bytes(c.size)}` : ""}
                </span>
              </button>
            ))}
            {collections.data?.length === 0 && <EmptyNote>No collections.</EmptyNote>}
          </div>
          {collections.data && collections.data.length > 0 && (
            <div className="numeric shrink-0 border-t border-hairline px-3 py-1.5 text-hint text-muted-foreground">
              {plural(collections.data.length, "collection")}
            </div>
          )}
        </div>

        <div className="flex min-h-0 min-w-0 flex-col">
          <PaneHeader className="gap-2">
            <span className="min-w-0 flex-1 truncate font-mono text-body font-medium">
              {collection ?? (
                <span className="font-sans text-muted-foreground">Pick a collection</span>
              )}
            </span>
            {collection && (
              <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
                {canWrite && (
                  <Button size="xs" variant="outline" onClick={() => setInserting(true)}>
                    <Plus className="size-3.5" />
                    Insert
                  </Button>
                )}
                {/* One verb inline, the rest behind one menu where each of
                    them gets a sentence (§13). Five buttons across a pane
                    header spelled "CSV" and "JSON" at each other and never
                    said what either would contain. */}
                <CollectionMenu
                  canWrite={canWrite}
                  filtered={applied.trim() !== "" && applied.trim() !== "{}"}
                  onExport={exportDocs}
                  onImport={() => setImporting(true)}
                  onDrop={dropCollection}
                />
              </div>
            )}
          </PaneHeader>

          {/* The same underlined strip every switcher in the product wears.
              The pill-shaped tab list it replaces was a control with a face,
              sitting on top of a workbench that has no other faces on it. */}
          <nav
            aria-label="Collection views"
            className="flex shrink-0 gap-1 overflow-x-auto border-b border-hairline px-2.5"
          >
            {VIEWS.map((entry) => (
              <button
                key={entry.key}
                type="button"
                aria-pressed={tab === entry.key}
                onClick={() => setTab(entry.key)}
                className={tabClasses(tab === entry.key, "h-9")}
              >
                {entry.label}
              </button>
            ))}
          </nav>

          {tab === "documents" && (
            <>
              {collection && (
                <div className="flex shrink-0 items-center gap-1.5 border-b border-hairline px-2.5 py-2">
                  <Input
                    value={filter}
                    onChange={(e) => setFilter(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && runFilter()}
                    className="h-7 min-w-0 flex-1 font-mono text-xs sm:h-7"
                    placeholder='{"status": "active"}'
                    aria-label="Filter document"
                  />
                  <Button size="xs" variant="outline" onClick={runFilter}>
                    <Play className="size-3.5" />
                    Find
                  </Button>
                </div>
              )}
              <div className="relative min-h-0 min-w-0 flex-1">
                {docs.error && <ErrorState error={docs.error} className="m-4" />}
                {!collection && <EmptyState icon={Layout} title="Select a collection" />}
                {docs.data && (
                  <ResultGrid
                    key={`${collection}:${applied}:${skip}`}
                    className="animate-rise"
                    result={docs.data}
                    onEdit={canWrite ? editDoc : undefined}
                    onDelete={canWrite ? deleteDoc : undefined}
                    maxHeightClass="h-full"
                    emptyTitle="No documents"
                    emptyDescription="Either the collection is empty or the filter above matched nothing in it."
                  />
                )}
              </div>
              {collection && (
                <PaneFooter className="justify-between">
                  <div className="flex items-center gap-1.5">
                    <Button
                      size="xs"
                      variant="outline"
                      disabled={skip === 0}
                      onClick={() => setSkip((s) => Math.max(0, s - PAGE))}
                    >
                      Previous
                    </Button>
                    <Button
                      size="xs"
                      variant="outline"
                      disabled={docs.data ? docs.data.rowCount < PAGE : true}
                      onClick={() => setSkip((s) => s + PAGE)}
                    >
                      Next
                    </Button>
                    {docs.data && docs.data.rowCount > 0 && (
                      <span className="numeric ml-1.5 text-hint text-muted-foreground">
                        Documents {(skip + 1).toLocaleString()}–
                        {(skip + docs.data.rowCount).toLocaleString()}
                        {docs.data.duration && ` · ${docs.data.duration}`}
                      </span>
                    )}
                  </div>
                  <Button size="xs" variant="ghost" onClick={reload} pending={docs.loading}>
                    Refresh
                  </Button>
                </PaneFooter>
              )}
            </>
          )}

          {tab === "indexes" && (
            <div className={cn("min-h-0 flex-1 overflow-y-auto", ringSafeScroll)}>
              {!collection && <EmptyState icon={CodeBracket} title="Select a collection" />}
              {info.data && (
                  <>
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Name</TableHead>
                          <TableHead>Keys</TableHead>
                          <TableHead>Unique</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {info.data.indexes.map((ix) => (
                          <TableRow key={ix.name}>
                            <TableCell className="font-mono">
                              {ix.name}
                              {ix.primary && <Tag className="ml-1.5">_id</Tag>}
                            </TableCell>
                            <TableCell className="font-mono text-muted-foreground">
                              {ix.columns.join(", ")}
                            </TableCell>
                            <TableCell>{ix.unique ? "yes" : "no"}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                    {info.data.stats && (
                      <div className="border-t border-hairline p-4">
                        <p className="mb-2 text-xs font-medium">Collection stats</p>
                        <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs sm:grid-cols-3">
                          {Object.entries(info.data.stats).map(([k, v]) => (
                            <div key={k} className="flex justify-between gap-2">
                              <span className="text-muted-foreground">{k}</span>
                              <span className="font-mono">{String(v)}</span>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </>
                )}
            </div>
          )}

          {tab === "aggregate" && (
            <div className={cn("min-h-0 flex-1 overflow-y-auto", ringSafeScroll, "p-4")}>
              <AggregateTab
                conn={conn}
                database={dbName}
                collection={collection}
                confirm={confirm}
                onWrote={collections.refresh}
              />
            </div>
          )}
        </div>
      </div>

      {editing && (
        <DocumentDialog
          title="Edit document"
          initial={editing.doc}
          onClose={() => setEditing(null)}
          onSave={(json) => saveDoc(json, editing.id)}
        />
      )}
      {importing && collection && (
        <ImportDialog
          open
          onOpenChange={(o) => !o && setImporting(false)}
          connId={conn.id}
          schema={dbName}
          table={collection}
          confirm={confirm}
          documentStore
          onDone={reload}
        />
      )}
      {inserting && (
        <DocumentDialog
          title="Insert document"
          // Braced, not a bare string attribute: JSX does not process escapes
          // in one, so `initial="{\n  \n}"` handed the editor the literal
          // characters backslash-n — the Insert dialog opened on invalid JSON,
          // showing a parse error and a disabled Save before anybody had typed.
          initial={"{\n  \n}"}
          onClose={() => setInserting(false)}
          onSave={insertDoc}
        />
      )}
    </Pane>
  )
}

/**
 * Everything that can be done to a collection but insert, behind one menu.
 *
 * The same shape the table workbench uses (`table-actions.tsx`), so the two
 * browse surfaces in this section do not disagree about where a verb lives.
 */
function CollectionMenu({
  canWrite,
  filtered,
  onExport,
  onImport,
  onDrop,
}: {
  canWrite: boolean
  /** Whether a filter is applied — the export carries it, and says so. */
  filtered: boolean
  onExport: (format: "csv" | "json") => void
  onImport: () => void
  onDrop: () => void
}) {
  const hint = `${filtered ? "What this filter matches" : "The whole collection"}, up to ${EXPORT_CAP.toLocaleString()} documents.`
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="icon-sm" variant="ghost" aria-label="More collection actions">
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        <DropdownMenuItem onClick={() => onExport("csv")}>
          <Download />
          <Words title="Export as CSV" hint={hint} />
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => onExport("json")}>
          <AcronymJson />
          <Words title="Export as JSON" hint={hint} />
        </DropdownMenuItem>
        {canWrite && (
          <>
            <DropdownMenuItem onClick={onImport}>
              <CloudUpload />
              <Words title="Import documents…" hint="CSV or JSON, in one transaction." />
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onClick={onDrop}>
              <Trash />
              <Words title="Drop collection…" hint="Deletes it and every document in it." />
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function Words({ title, hint }: { title: string; hint: string }) {
  return (
    <span className="flex min-w-0 flex-col">
      <span>{title}</span>
      <span className="text-hint text-muted-foreground">{hint}</span>
    </span>
  )
}

function DocumentDialog({
  title,
  initial,
  onClose,
  onSave,
}: {
  title: string
  initial: string
  onClose: () => void
  onSave: (json: string) => Promise<void>
}) {
  const [json, setJson] = useState(initial)
  const [busy, setBusy] = useState(false)

  // Parsed on every keystroke so a malformed document is caught here rather
  // than by the server after a round trip.
  const parseError = useMemo(() => {
    try {
      JSON.parse(json)
      return null
    } catch (e) {
      return String(e instanceof Error ? e.message : e)
    }
  }, [json])

  const save = async () => {
    setBusy(true)
    try {
      await onSave(json)
    } catch (err) {
      notify.error("Could not save", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      size="lg"
      title={title}
      description="The document as JSON. It is checked as you type and replaced whole when saved."
      footer={
        <>
          {parseError ? (
            <FormNote tone="danger" className="mr-auto truncate">
              {parseError}
            </FormNote>
          ) : (
            <FormNote className="mr-auto">Valid JSON</FormNote>
          )}
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={Boolean(parseError) || busy} pending={busy}>
            Save
          </Button>
        </>
      }
    >
      <div className="grid gap-3">
        <div className="overflow-hidden rounded-lg border border-hairline">
          <CodeEditor className="h-80" language="json" value={json} onChange={setJson} />
        </div>
        <FormNote>
          The whole document is replaced. <span className="font-mono">_id</span> is immutable and is
          ignored if present.
        </FormNote>
      </div>
    </Modal>
  )
}

function AggregateTab({
  conn,
  database,
  collection,
  confirm,
  // A $out or $merge pipeline creates or replaces a collection, which the list
  // beside this tab has no other way to hear about: it polls on demand only, so
  // the collection somebody just built stayed invisible until they switched
  // database or reloaded the page.
  onWrote,
}: {
  conn: DbConnection
  database: string
  collection?: string
  confirm: ConfirmFn
  onWrote: () => void
}) {
  const { can } = useAuth()
  const [pipeline, setPipeline] = useState('[\n  { "$match": {} },\n  { "$limit": 20 }\n]')
  const [result, setResult] = useState<QueryResult | null>(null)
  const [busy, setBusy] = useState(false)

  // $out and $merge rewrite a collection. Flagging that here mirrors what the
  // SQL editor does with a destructive statement: the warning appears before
  // the pipeline is ever sent, not after it has run.
  const writes = /\$out|\$merge/.test(pipeline)

  const execute = async (confirmText?: string) => {
    setBusy(true)
    try {
      const res = await post<{ result: QueryResult }>(
        `/databases/${conn.id}/aggregate`,
        { database, collection, pipeline, limit: 200 },
        { confirm: confirmText },
      )
      setResult(res.result)
      notify.success(`${plural(res.result.rowCount, "document")} in ${res.result.duration}`)
      if (writes) onWrote()
    } catch (err) {
      notify.error("Pipeline failed", err)
      throw err
    } finally {
      setBusy(false)
    }
  }

  const run = () => {
    if (writes) {
      confirm({
        title: "Run a writing pipeline",
        phrase: "run pipeline",
        confirmLabel: "Run it",
        description: (
          <p className="text-destructive">
            This pipeline ends in $out or $merge, which replaces or updates a whole collection.
          </p>
        ),
        action: (c) => execute(c),
      })
      return
    }
    execute().catch(() => undefined)
  }

  if (!collection) return <EmptyState icon={AcronymJson} title="Select a collection" />

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <Panel plain>
        <PanelHeader title="Aggregation pipeline" />
        <PanelBody flush>
          <CodeEditor className="h-56" language="json" value={pipeline} onChange={setPipeline} />
        </PanelBody>
        <PanelFooter>
          <Button size="sm" onClick={run} disabled={busy || !can("service.control")} pending={busy}>
            <Play className="size-3.5" />
            Run
          </Button>
          {writes && <span className="text-xs text-destructive">writes a collection</span>}
        </PanelFooter>
      </Panel>
      {result && (
        <Panel>
          <PanelHeader title="Result" />
          <PanelBody flush>
            <ResultGrid
              result={result}
              emptyTitle="The pipeline ran and returned nothing"
              emptyDescription="Not an error — every stage completed and the last one emitted no documents."
            />
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}
