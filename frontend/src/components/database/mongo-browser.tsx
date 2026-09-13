"use client"

import { useMemo, useState } from "react"
import {
  AcronymJson,
  CloudUpload,
  CodeBracket,
  Download,
  Layout,
  Play,
  Plus,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, downloadUrl, get, patch, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { cn, ringSafeScroll } from "@/lib/utils"
import type { DbConnection, DbTable, MongoCollectionInfo, QueryResult } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
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

type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]
const PAGE = 100

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
  const [database, setDatabase] = useState(conn.database)
  const [collection, setCollection] = useState<string>()
  const [filter, setFilter] = useState("{}")
  const [applied, setApplied] = useState("{}")
  const [skip, setSkip] = useState(0)
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
    <div className="grid gap-4 lg:grid-cols-[16rem_minmax(0,1fr)] [&>*]:min-w-0">
      <Panel>
        <PanelHeader title="Collections" />
        <PanelBody className="space-y-3">
          {databases.data && databases.data.length > 1 && (
            <Select
              value={dbName}
              onValueChange={(v) => {
                setDatabase(v)
                setCollection(undefined)
                setSkip(0)
              }}
            >
              <SelectTrigger size="sm" className="w-full">
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
          )}
          <div
            className={cn("max-h-[calc(100svh-28rem)] space-y-0.5 overflow-y-auto", ringSafeScroll)}
          >
            {collections.loading && <LoadingRows rows={4} />}
            {collections.data?.map((c) => (
              <button
                key={c.name}
                onClick={() => {
                  setCollection(c.name)
                  setSkip(0)
                  setFilter("{}")
                  setApplied("{}")
                }}
                className={cn(
                  "flex w-full min-w-0 flex-col rounded-md px-2 py-1.5 text-left transition-colors",
                  collection === c.name
                    ? "bg-plot-primary font-medium text-foreground"
                    : "hover:bg-accent",
                )}
              >
                <span className="truncate text-body">{c.name}</span>
                <span className="truncate text-hint text-muted-foreground">
                  {c.estimatedRows.toLocaleString()} docs
                  {c.size ? ` · ${bytes(c.size)}` : ""}
                </span>
              </button>
            ))}
            {collections.data?.length === 0 && <EmptyNote>No collections.</EmptyNote>}
          </div>
        </PanelBody>
      </Panel>

      <div className="flex min-w-0 flex-col gap-4">
        <Tabs value={tab} onValueChange={setTab} className="min-w-0 gap-4">
          <TabsList>
            <TabsTrigger value="documents">Documents</TabsTrigger>
            <TabsTrigger value="indexes">Indexes</TabsTrigger>
            <TabsTrigger value="aggregate">Aggregate</TabsTrigger>
          </TabsList>

          <TabsContent value="documents" className="min-w-0">
            <Panel>
              <PanelHeader
                title={collection ?? "Pick a collection"}
                actions={
                  collection && (
                    <>
                      {canWrite && (
                        <Button size="sm" variant="outline" onClick={() => setInserting(true)}>
                          <Plus className="size-3.5" />
                          Insert
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="ghost"
                        title="Export the current filter as CSV"
                        onClick={() => exportDocs("csv")}
                      >
                        <Download className="size-3.5" />
                        CSV
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        title="Export the current filter as JSON"
                        onClick={() => exportDocs("json")}
                      >
                        <AcronymJson className="size-3.5" />
                        JSON
                      </Button>
                      {canWrite && (
                        <Button size="sm" variant="ghost" onClick={() => setImporting(true)}>
                          <CloudUpload className="size-3.5" />
                          Import
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={skip === 0}
                        onClick={() => setSkip((s) => Math.max(0, s - PAGE))}
                      >
                        Previous
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={docs.data ? docs.data.rowCount < PAGE : true}
                        onClick={() => setSkip((s) => s + PAGE)}
                      >
                        Next
                      </Button>
                      {canWrite && (
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-destructive"
                          onClick={dropCollection}
                        >
                          <Trash className="size-3.5" />
                        </Button>
                      )}
                    </>
                  )
                }
              />
              {collection && (
                <PanelToolbar>
                  <Input
                    value={filter}
                    onChange={(e) => setFilter(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && runFilter()}
                    className="h-8 font-mono text-xs"
                    placeholder='{"status": "active"}'
                  />
                  <Button size="sm" variant="outline" onClick={runFilter}>
                    <Play className="size-3.5" />
                    Find
                  </Button>
                </PanelToolbar>
              )}
              <PanelBody flush>
                {docs.error && <ErrorState error={docs.error} className="m-4" />}
                {!collection && <EmptyState icon={Layout} title="Select a collection" />}
                {docs.data && (
                  <ResultGrid
                    result={docs.data}
                    onEdit={canWrite ? editDoc : undefined}
                    onDelete={canWrite ? deleteDoc : undefined}
                    emptyTitle="No documents"
                    emptyDescription="Either the collection is empty or the filter above matched nothing in it."
                  />
                )}
              </PanelBody>
            </Panel>
          </TabsContent>

          <TabsContent value="indexes" className="min-w-0">
            <Panel>
              <PanelHeader title="Indexes" />
              <PanelBody flush>
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
              </PanelBody>
            </Panel>
          </TabsContent>

          <TabsContent value="aggregate" className="min-w-0">
            <AggregateTab
              conn={conn}
              database={dbName}
              collection={collection}
              confirm={confirm}
              onWrote={collections.refresh}
            />
          </TabsContent>
        </Tabs>
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
    </div>
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
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={Boolean(parseError) || busy} pending={busy}>
            Save
          </Button>
        </>
      }
    >
      <div className="space-y-2">
        <CodeEditor className="h-80" language="json" value={json} onChange={setJson} />
        {parseError && <p className="text-xs text-destructive">{parseError}</p>}
        <p className="text-xs text-muted-foreground">
          The whole document is replaced. <span className="font-mono">_id</span> is immutable and is
          ignored if present.
        </p>
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
      <Panel>
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
