"use client"

import { useEffect, useMemo, useState } from "react"
import {
  ChevronDoubleDown,
  ChevronLeft,
  ChevronRight,
  CloudUpload,
  Copy,
  Download,
  Eye,
  Fingerprint,
  Pencil,
  Plus,
  PlusSquareSmall,
  RefreshClockwise,
  SettingsSliders,
  StopCircle,
  Trash,
} from "@/components/icons"
import { ApiError } from "@/lib/api"
import { bytes } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useMemoryState, useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { FormFact, FormFacts } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { PaneFooter } from "@/components/panel"
import { EmptyState } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { VerbMenu, type Verb } from "@/components/verbs"
import { grouped } from "@/components/database/data/view"
import { useGridLayout } from "@/components/database/grid"
import { EngineMark } from "@/components/database/kit"
import {
  cloneDocument,
  countDocuments,
  deleteDocuments,
  findDocuments,
} from "@/components/database/mongo/api"
import { idLabel, parseDocument, printShell } from "@/components/database/mongo/bson"
import {
  NO_STAGED,
  idsFilter,
  stagedKey,
  withStaged,
  withoutStaged,
  type Edits,
  type StagedEdits,
} from "@/components/database/mongo/changes"
import { BulkDialog, type BulkMode } from "@/components/database/mongo/documents/bulk"
import {
  EditDocumentDialog,
  InsertDocumentDialog,
  editorText,
} from "@/components/database/mongo/documents/document-editor"
import { QueryBar } from "@/components/database/mongo/documents/query-bar"
import { sortOf, sortText } from "@/components/database/mongo/documents/table"
import {
  JsonView,
  ListView,
  TableView,
  useListed,
} from "@/components/database/mongo/documents/views"
import { ExplainDialog, type ExplainSubject } from "@/components/database/mongo/explain"
import { Stopped, useElapsed, useStoppable } from "@/components/database/mongo/in-flight"
import {
  CollectionKindTag,
  CollectionMark,
  collectionKind,
} from "@/components/database/mongo/kinds"
import {
  DEFAULT_PAGE,
  EMPTY_QUERY,
  PAGE_SIZES,
  QUERY_FIELDS,
  addressOf,
  collectionKey,
  draftFromAddress,
  draftProblems,
  findSpec,
  findStatement,
  isEmptyDraft,
  isFiltered,
  pageWindow,
  queryMemoryKey,
  rangeWords,
  refusedField,
  withHistory,
  type HistoryEntry,
  type QueryDraft,
} from "@/components/database/mongo/query"
import { mergeFilter } from "@/components/database/mongo/shell"
import { ImportDialog } from "@/components/database/mongo/transfer"
import type { MongoDoc, MongoFindResult } from "@/components/database/mongo/types"
import {
  MongoWorkbench,
  WorkbenchHead,
  type Workbench,
} from "@/components/database/mongo/workbench"
import { ReadError } from "@/components/database/redis/read-error"

/** The documents before they land: a few of them, each a short run of fields. */
function DocumentsSkeleton() {
  return (
    <div aria-hidden className="divide-y divide-hairline">
      {[0, 1, 2, 3].map((block) => (
        <div key={block} className="space-y-2.5 px-3 py-3 pl-8">
          {[52, 36, 28, 44, 32].map((width, line) => (
            <Skeleton key={line} className="h-3" style={{ width: `${width - block * 3}%` }} />
          ))}
        </div>
      ))}
    </div>
  )
}

type View = "list" | "json" | "table"
const VIEWS: { id: View; label: string }[] = [
  { id: "list", label: "List" },
  { id: "json", label: "JSON" },
  { id: "table", label: "Table" },
]

/**
 * Documents: the collections of a database beside the documents of the one
 * that is open, found with MongoDB's own query bar and read three ways.
 *
 * The database, the collection, the query and the page are in the address, so
 * a pasted link opens on the same documents; opening a collection is a step
 * of history, so Back returns to the one before. A document is edited as the
 * Extended JSON it is stored as — nothing here passes it through a JavaScript
 * number or a plain object, which is how the old editor rewrote a document's
 * types by saving it — or a field at a time, as the update that change is.
 *
 * Edits made a field at a time are staged on their documents and stay there
 * until they are sent or let go: turning the page, asking another query,
 * opening another collection or another page of the database takes nothing
 * away. The strip over the documents says how many hold unsent edits and
 * brings the ones that are not on screen back.
 */
export function MongoDocuments() {
  return (
    <MongoWorkbench section="data">
      {(workbench) => (
        <Documents
          // A collection is one page's worth of state: another starts clean.
          key={collectionKey(workbench.mongo.database, workbench.mongo.collection)}
          {...workbench}
        />
      )}
    </MongoWorkbench>
  )
}

function Documents({
  mongo,
  catalog,
  collection: info,
  leading,
  confirm,
  exportCollection,
  exporting,
  collectionOptions,
}: Workbench) {
  const { id, target, database, collection, param, select, engine, canWrite, canDestroy } = mongo
  const view_ = info?.type === "view"

  /* ------------------------------------------------------------ the query */

  const addressKey = QUERY_FIELDS.map((field) => param(field)).join("\u0000")
  // eslint-disable-next-line react-hooks/exhaustive-deps -- `param` reads the address; the key is what it says
  const applied = useMemo(() => draftFromAddress(param), [addressKey])
  const [draft, setDraft] = useState(applied)
  const [seen, setSeen] = useState(addressKey)
  if (seen !== addressKey) {
    // The address moved under the page — Back, Forward, a link from Schema:
    // the bar shows the query that is now on screen.
    setSeen(addressKey)
    setDraft(applied)
  }
  const [optionsOpen, setOptionsOpen] = useViewState(`databases.${id}.mongo.options`, false)
  const [history, setHistory] = useSessionState<readonly HistoryEntry[]>(
    `databases.${id}.mongo.history`,
    [],
  )
  // What Schema's "add this value to the filter" builds on: the query this
  // collection was last asked.
  const [, setMemory] = useSessionState<Record<string, QueryDraft>>(queryMemoryKey(id), {})
  useEffect(() => {
    // Whatever put the query in the address — Find, a link from Schema, a
    // pasted address, Back — it is the query the collection was last asked.
    setMemory((held) => ({ ...held, [collectionKey(database, collection)]: applied }))
  }, [applied, database, collection, setMemory])
  const [epoch, setEpoch] = useState(0)

  const [size, setSize] = useViewState<number>(`databases.${id}.mongo.pageSize`, DEFAULT_PAGE)
  const pageSize = (PAGE_SIZES as readonly number[]).includes(size) ? size : DEFAULT_PAGE
  const asked = Number(param("page"))
  const window_ = pageWindow(
    applied,
    Number.isInteger(asked) && asked > 1 ? asked - 1 : 0,
    pageSize,
  )

  const [expanded, setExpanded] = useViewState(`databases.${id}.mongo.expanded`, false)
  const [preferred, setPreferred] = useViewState<View>(`databases.${id}.mongo.view`, "list")
  const named = param("view")
  const view: View = VIEWS.some((entry) => entry.id === named)
    ? (named as View)
    : VIEWS.some((entry) => entry.id === preferred)
      ? preferred
      : "list"

  /* ------------------------------------------------------------- the read */

  const spec = useMemo(
    () => findSpec(applied, { skip: window_.skip, limit: window_.limit }),
    [applied, window_.skip, window_.limit],
  )
  const identity = JSON.stringify([database, collection, spec, epoch])
  // A find the reader can stop: after a second the bar says how long it has
  // run, and Stop drops the request — and with it the server's work on it.
  const flight = useStoppable()
  const poll = usePoll(
    (signal) => flight.run((own) => findDocuments(target, spec, false, own), signal),
    0,
    [id, identity],
  )
  const seconds = useElapsed(poll.loading)
  // The page on screen is kept under the one being read, so turning a page
  // sweeps over the old documents instead of collapsing to a skeleton.
  const [kept, setKept] = useState<MongoFindResult>()
  if (poll.data && poll.data !== kept) setKept(poll.data)
  const result = poll.data ?? kept
  const current = poll.data !== undefined

  // Counted beside the page and only when the filter changes: turning a page
  // does not count again.
  const countIdentity = JSON.stringify([
    database,
    collection,
    applied.filter,
    applied.collation,
    applied.hint,
    epoch,
  ])
  const counted = usePoll(
    (signal) =>
      countDocuments(
        target,
        {
          filter: applied.filter.trim() ? applied.filter : undefined,
          collation: applied.collation.trim() ? applied.collation : undefined,
          hint: applied.hint.trim() ? applied.hint : undefined,
        },
        signal,
      ),
    0,
    [id, countIdentity],
  )

  const listed = useListed(result?.documents ?? [])
  const [gridLayout, setGridLayout] = useGridLayout(
    `databases.${id}.mongo.grid.${database}.${collection}`,
  )

  /* ------------------------------------------------------- staged edits */

  // Held for the tab, by collection and by document, and never written down:
  // they are values of the documents. What is here is exactly what has not
  // been sent, wherever the reader has been in between.
  const [allStaged, setAllStaged] = useMemoryState<StagedEdits>(stagedKey(id), {})
  const scope = collectionKey(database, collection)
  const edits = allStaged[scope] ?? NO_STAGED
  const stagedIds = Object.keys(edits)
  const setEdits = (document: string, next: Edits) =>
    setAllStaged((held) => withStaged(held, scope, document, next))
  // Staged on documents the page on screen does not hold: said, and brought back on request.
  const onPage = new Set(poll.data?.documents.map((doc) => doc.id))
  const elsewhere = poll.data ? stagedIds.filter((staged) => !onPage.has(staged)) : []

  /* ------------------------------------------------------------- dialogs */

  const [editing, setEditing] = useState<MongoDoc | null>(null)
  const [inserting, setInserting] = useState<{ initial?: string } | null>(null)
  const [importing, setImporting] = useState(false)
  const [bulk, setBulk] = useState<BulkMode | null>(null)
  const [explaining, setExplaining] = useState<ExplainSubject | null>(null)

  const reread = () => {
    setEpoch((n) => n + 1)
    catalog.refresh()
  }

  /* ------------------------------------------------------------- actions */

  const apply = (next: QueryDraft) => {
    setHistory((held) => withHistory(held, { database, collection, draft: next, at: Date.now() }))
    // Like a filter or a sort on the SQL pages, a query replaces the address
    // rather than adding to history: Back is the collection before, not every
    // query typed on the way. It is taken at once — the page does not wait
    // for the router to show it.
    select({ ...addressOf(next), page: null })
    // The same query asked again is still a fresh read.
    setEpoch((n) => n + 1)
  }
  const find = (next: QueryDraft = draft) => {
    if (Object.keys(draftProblems(next)).length > 0) return
    apply(next)
  }
  const ask = (next: QueryDraft) => {
    setDraft(next)
    apply(next)
  }
  const reset = () => ask(EMPTY_QUERY)
  const turn = (to: number) => select({ page: to <= 0 ? null : String(to + 1) })
  const narrow = (clause: string) =>
    ask({ ...applied, filter: mergeFilter(applied.filter, clause) })
  /** The documents with unsent edits, found by their ids. */
  const showStaged = () => ask({ ...EMPTY_QUERY, filter: idsFilter(stagedIds) })
  const discardStaged = () => {
    const before = edits
    setAllStaged((held) => withoutStaged(held, scope))
    notify.info(
      stagedIds.length === 1
        ? "The unsent edits of one document were let go"
        : `The unsent edits of ${stagedIds.length} documents were let go`,
      {
        action: {
          label: "Undo",
          onClick: () => setAllStaged((held) => ({ ...held, [scope]: before })),
        },
      },
    )
  }
  /** The order a head of the table asks for, written into the bar's Sort and asked of the server. */
  const sortBy = (sort: Parameters<typeof sortText>[0]) => {
    const text = sortText(sort)
    if (text === null) {
      notify.info("A field with a dot or a leading $ in its name cannot be sorted by")
      return
    }
    ask({ ...applied, sort: text })
  }
  const setView = (next: View) => {
    setPreferred(next)
    select({ view: next })
  }

  const verbsFor = (doc: MongoDoc): Verb[] => {
    const label = idLabel(doc.id)
    return [
      canWrite && !view_
        ? { key: "edit", label: "Edit", icon: Pencil, inline: true, run: () => setEditing(doc) }
        : { key: "view", label: "Open", icon: Eye, inline: true, run: () => setEditing(doc) },
      ...(canWrite && !view_ && doc.id
        ? [
            {
              key: "clone",
              label: "Clone",
              icon: PlusSquareSmall,
              inline: true,
              progressive: "Cloning…",
              run: () => {
                cloneDocument(target, doc.id)
                  .then(({ document: copy }) => {
                    notify.success("Document cloned", {
                      description: `The copy is ${idLabel(copy.id)}.`,
                      action: { label: "Edit the copy", onClick: () => setEditing(copy) },
                    })
                    reread()
                  })
                  .catch((err: unknown) => notify.error("Could not clone the document", err))
              },
            },
          ]
        : []),
      {
        key: "copy",
        label: "Copy",
        icon: Copy,
        inline: true,
        run: () => void copyText(editorText(doc.canonical), "Document copied as Extended JSON"),
      },
      {
        key: "copy-shell",
        label: "Copy as shell syntax",
        icon: Copy,
        run: () => {
          try {
            void copyText(printShell(parseDocument(doc.canonical), true), "Document copied")
          } catch {
            void copyText(doc.canonical, "Document copied")
          }
        },
      },
      ...(doc.id
        ? [
            {
              key: "copy-id",
              label: "Copy _id",
              icon: Fingerprint,
              run: () => void copyText(label, "_id copied"),
            },
          ]
        : []),
      ...(canDestroy && !view_ && doc.id
        ? [
            {
              key: "delete",
              label: "Delete",
              icon: Trash,
              danger: true,
              run: () =>
                confirm({
                  title: "Delete document",
                  description: "The document is deleted. This cannot be undone.",
                  subject: {
                    mark: <EngineMark engine={engine} size="sm" />,
                    name: <span className="font-mono">{label}</span>,
                    facts: (
                      <FormFacts>
                        <FormFact label="Collection" mono>
                          {database}.{collection}
                        </FormFact>
                        <FormFact label="Size">{bytes(doc.size)}</FormFact>
                      </FormFacts>
                    ),
                  },
                  confirmLabel: "Delete document",
                  action: async () => {
                    await deleteDocuments(target, { id: doc.id })
                    notify.success("Document deleted")
                    return "reported"
                  },
                  onDone: () => {
                    // Edits staged on a document that is gone are edits of nothing.
                    setEdits(doc.id, {})
                    reread()
                  },
                }),
            },
          ]
        : []),
    ]
  }

  const pageVerbs: Verb[] = [
    ...(canWrite && !view_
      ? [
          {
            key: "update",
            label: "Update documents…",
            icon: Pencil,
            run: () => setBulk("update"),
          },
        ]
      : []),
    {
      key: "copy-query",
      label: "Copy the query",
      icon: Copy,
      run: () => void copyText(findStatement(collection, applied), "Query copied"),
    },
    ...(collectionOptions
      ? [
          {
            key: "options",
            label: "Collection options…",
            icon: SettingsSliders,
            run: collectionOptions,
          },
        ]
      : []),
    ...(canDestroy && !view_
      ? [
          {
            key: "delete",
            label: "Delete documents…",
            icon: Trash,
            danger: true,
            run: () => setBulk("delete"),
          },
        ]
      : []),
  ]

  /* ------------------------------------------------------------ problems */

  const typed = draftProblems(draft)
  // The server's refusal belongs to the query it was given: once the bar is
  // edited away from that, the sentence is about something else.
  const untouched = QUERY_FIELDS.every((field) => draft[field] === applied[field])
  // A refusal is the server's answer to this query (a 400, or a read that ran
  // past its time limit); anything else is a read that failed for a reason
  // the query has no part in.
  const failure = poll.error
  const stopped = failure instanceof Stopped
  const refused =
    failure instanceof ApiError && (failure.status === 400 || failure.code === "query_timeout")
  const refusal =
    failure && refused && untouched
      ? refusedField(failure.message, applied, failure instanceof ApiError ? failure.code : "")
      : null
  const problems = refusal?.field ? { ...typed, [refusal.field]: refusal.message } : typed

  /* --------------------------------------------------------------- foot */

  const filtered = isFiltered(applied)
  const count = counted.data ?? null
  // Where the reader's own Skip puts page one; a page's place is counted from there.
  const base = window_.skip - window_.page * pageSize
  const range = result
    ? rangeWords(result.skip - base, result.returned, count, filtered, window_.cap)
    : ""
  // A page that could not be read leaves the one before on screen: its range is said as that.
  const words = failure && result ? `${range} · from before` : range
  const hasNext = Boolean(result?.hasMore) && !window_.last
  const pager = (
    <>
      {result && (
        <span className="numeric text-hint whitespace-nowrap text-muted-foreground max-lg:hidden">
          {result.durationMs.toLocaleString("en-US")} ms
        </span>
      )}
      {result?.truncated && (
        <span
          className="text-hint whitespace-nowrap text-warning"
          title="A page ends at 8 MiB of documents, and this one ended there before it was full. Choose fewer documents a page to read the ones it left out."
        >
          page cut at 8 MiB
        </span>
      )}
      <span
        className="numeric text-hint whitespace-nowrap text-muted-foreground"
        aria-live="polite"
      >
        {words}
      </span>
      <Select
        value={String(pageSize)}
        onValueChange={(next) => {
          setSize(Number(next))
          select({ page: null })
        }}
      >
        <SelectTrigger
          size="sm"
          aria-label="Documents a page"
          className="h-7 gap-1 px-2 text-xs data-[size=sm]:h-7 sm:data-[size=sm]:h-7"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent position="popper" align="end">
          {PAGE_SIZES.map((option) => (
            <SelectItem key={option} value={String(option)} className="text-xs">
              {option} a page
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <div className="flex items-center">
        <IconAction
          label="Previous page"
          className="size-7"
          disabled={window_.page === 0}
          onClick={() => turn(window_.page - 1)}
        >
          <ChevronLeft />
        </IconAction>
        <IconAction
          label="Next page"
          className="size-7"
          disabled={!hasNext}
          onClick={() => turn(window_.page + 1)}
        >
          <ChevronRight />
        </IconAction>
      </div>
    </>
  )

  const empty = result !== undefined && result.returned === 0
  const kind = info ? collectionKind(info) : "collection"

  return (
    <div data-slot="mongo-documents" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <WorkbenchHead leading={leading}>
        <h2 className="flex min-w-0 items-center gap-1.5">
          <CollectionMark kind={kind} />
          <span className="min-w-0 truncate font-mono text-sm leading-6 font-medium">
            {collection}
          </span>
        </h2>
        <CollectionKindTag kind={kind} />
        {info && (
          <span className="numeric min-w-0 truncate text-hint text-muted-foreground max-md:hidden">
            {info.type === "view"
              ? `on ${info.viewOn}`
              : info.statsKnown
                ? `${grouped(info.count)} ${info.count === 1 ? "document" : "documents"} · ${bytes(info.size)}`
                : ""}
          </span>
        )}
        <span className="min-w-0 flex-1" />
        <div className="flex min-w-0 flex-wrap items-center gap-1">
          {canWrite && !view_ && (
            <Button size="xs" variant="outline" onClick={() => setInserting({})}>
              <Plus />
              Insert
            </Button>
          )}
          {canWrite && !view_ && engine.can("import") && (
            <Button size="xs" variant="ghost" onClick={() => setImporting(true)}>
              <CloudUpload />
              Import
            </Button>
          )}
          {engine.can("export") && (
            <Button
              size="xs"
              variant="ghost"
              pending={exporting}
              onClick={() => exportCollection(isEmptyDraft(applied) ? undefined : applied)}
            >
              <Download />
              Export
            </Button>
          )}
          <IconAction label="Read the documents again" className="size-7" onClick={reread}>
            <RefreshClockwise />
          </IconAction>
          <VerbMenu verbs={pageVerbs} label={`More actions for ${collection}`} />
        </div>
      </WorkbenchHead>

      <QueryBar
        draft={draft}
        onChange={setDraft}
        problems={problems}
        refusal={refusal && !refusal.field ? refusal.message : undefined}
        optionsOpen={optionsOpen}
        onOptionsOpen={setOptionsOpen}
        running={poll.loading}
        seconds={seconds}
        onStop={flight.stop}
        onFind={() => find()}
        onReset={reset}
        onExplain={
          engine.can("explainJSON")
            ? () =>
                setExplaining({
                  kind: "find",
                  // The reader's own skip and limit are part of the plan; the page's are not.
                  spec: findSpec(draft, {
                    skip: Number(draft.skip) || 0,
                    limit: Number(draft.limit) || 0,
                  }),
                  statement: findStatement(collection, draft),
                })
            : undefined
        }
        history={history.filter(
          (entry) => entry.database === database && entry.collection === collection,
        )}
        onPick={ask}
        onClearHistory={() =>
          setHistory((held) =>
            held.filter(
              (entry) => !(entry.database === database && entry.collection === collection),
            ),
          )
        }
      />

      <div className="flex min-h-9 shrink-0 flex-wrap items-center gap-x-3 border-b border-hairline px-3">
        <div role="group" aria-label="Document views" className="flex h-9 gap-1">
          {VIEWS.map((entry) => (
            <button
              key={entry.id}
              type="button"
              aria-pressed={view === entry.id}
              onClick={() => setView(entry.id)}
              className={tabClasses(view === entry.id, "h-9")}
            >
              {entry.label}
            </button>
          ))}
        </div>
        {view === "list" && (
          <Button
            size="xs"
            variant="ghost"
            aria-pressed={expanded}
            className={cn(expanded && "bg-accent")}
            onClick={() => setExpanded(!expanded)}
          >
            <ChevronDoubleDown />
            Expand all
          </Button>
        )}
        {stagedIds.length > 0 && (
          <div
            data-slot="mongo-unsent"
            className="ml-auto flex min-h-9 min-w-0 flex-wrap items-center gap-x-1.5"
          >
            <p className="text-hint text-(--git-modified)">
              Unsent edits on{" "}
              {stagedIds.length === 1 ? "one document" : `${stagedIds.length} documents`}
              {elsewhere.length > 0 &&
                (elsewhere.length === stagedIds.length
                  ? ", not on this page"
                  : `, ${elsewhere.length} of them not on this page`)}
            </p>
            {elsewhere.length > 0 && (
              <Button size="xs" variant="ghost" onClick={showStaged}>
                Show {stagedIds.length === 1 ? "it" : "them"}
              </Button>
            )}
            <Button size="xs" variant="ghost" onClick={discardStaged}>
              Let them go
            </Button>
          </div>
        )}
      </div>

      {/* A read that failed over documents already on screen: they stay, and
          the line says whose they are, why the read failed, and asks again. */}
      {failure && result && (
        <div
          role="status"
          data-slot="mongo-read-failed"
          className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hairline px-3 py-1.5"
        >
          <p className="min-w-0 flex-1 basis-64 text-hint leading-relaxed break-words text-muted-foreground">
            <span className="font-medium text-warning">
              {stopped
                ? "Stopped."
                : refused
                  ? "The server refused this query."
                  : window_.page > 0
                    ? `Page ${window_.page + 1} could not be read.`
                    : "The documents could not be read again."}
            </span>{" "}
            {!stopped && <span className="font-mono">{failure.message}</span>} These are the
            documents from before.
          </p>
          <Button size="xs" variant="outline" onClick={poll.refresh}>
            {stopped ? "Find again" : "Try again"}
          </Button>
        </div>
      )}

      {/* The sweep over a page that is being replaced: the old documents stay under it. */}
      <div className="relative min-h-0 flex-1">
        {poll.loading && result && (
          <div
            aria-hidden
            className="absolute inset-x-0 top-0 z-10 h-0.5 overflow-hidden bg-meter-track"
          >
            <div className="h-full w-1/3 animate-sweep bg-brand" />
          </div>
        )}
        {view === "table" && result && !empty ? (
          <div className="absolute inset-0 flex min-h-0 min-w-0 flex-col">
            <TableView
              label={`${collection} documents`}
              listed={listed}
              layout={gridLayout}
              onLayoutChange={setGridLayout}
              offset={result.skip}
              loading={poll.loading && !current}
              sort={sortOf(applied.sort)}
              onSort={sortBy}
              onOpen={setEditing}
              onFilter={narrow}
              footer={pager}
            />
          </div>
        ) : (
          <div className="absolute inset-0 overflow-x-hidden overflow-y-auto">
            {stopped && !result ? (
              // Stopping is the reader's own doing, not a failure: it is said plainly.
              <EmptyState
                icon={StopCircle}
                className="m-4 border-0"
                title="Stopped"
                description="The find was stopped before the server answered."
                action={
                  <Button size="sm" variant="outline" onClick={poll.refresh}>
                    Find again
                  </Button>
                }
              />
            ) : poll.error && !result ? (
              <ReadError error={poll.error} onRetry={poll.refresh} className="m-4" />
            ) : !result ? (
              <DocumentsSkeleton />
            ) : empty ? (
              <EmptyState
                mark={<EngineMark engine={engine} />}
                className="m-4 border-0"
                title={
                  filtered
                    ? "No document matches"
                    : window_.page > 0
                      ? "No documents on this page"
                      : `${collection} holds no documents`
                }
                description={
                  filtered
                    ? "Nothing in the collection matches this filter."
                    : window_.page > 0
                      ? "The collection ends before this page."
                      : canWrite && !view_
                        ? "Insert the first one, or import a file."
                        : undefined
                }
                action={
                  filtered || window_.page > 0 ? (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => (filtered ? reset() : turn(0))}
                    >
                      {filtered ? "Reset the query" : "Go to the first page"}
                    </Button>
                  ) : (
                    canWrite &&
                    !view_ && (
                      <div className="flex flex-wrap justify-center gap-2">
                        <Button size="sm" onClick={() => setInserting({})}>
                          <Plus />
                          Insert a document
                        </Button>
                        {engine.can("import") && (
                          <Button size="sm" variant="outline" onClick={() => setImporting(true)}>
                            <CloudUpload />
                            Import a file
                          </Button>
                        )}
                      </div>
                    )
                  )
                }
              />
            ) : view === "json" ? (
              <JsonView listed={listed} verbsFor={verbsFor} />
            ) : (
              <ListView
                target={target}
                listed={listed}
                expanded={expanded}
                editable={canWrite && !view_}
                edits={edits}
                onEdits={setEdits}
                verbsFor={verbsFor}
                onUpdated={reread}
              />
            )}
          </div>
        )}
      </div>

      {/* The table carries the page's controls in its own status strip. */}
      {!(view === "table" && result && !empty) && (
        <PaneFooter className="justify-end gap-3">{pager}</PaneFooter>
      )}

      <EditDocumentDialog
        mongo={mongo}
        target={target}
        document={editing}
        onOpenChange={(open) => !open && setEditing(null)}
        onSaved={reread}
      />
      {canWrite && (
        <InsertDocumentDialog
          mongo={mongo}
          target={target}
          open={inserting !== null}
          initial={inserting?.initial}
          onOpenChange={(open) => !open && setInserting(null)}
          onInserted={reread}
        />
      )}
      {canWrite && (
        <ImportDialog
          mongo={mongo}
          target={target}
          open={importing}
          onOpenChange={setImporting}
          onImported={reread}
        />
      )}
      {canWrite && (
        <BulkDialog
          mongo={mongo}
          target={target}
          mode={bulk}
          initialFilter={applied.filter}
          confirm={confirm}
          onOpenChange={(open) => !open && setBulk(null)}
          onDone={reread}
        />
      )}
      <ExplainDialog
        mongo={mongo}
        target={target}
        subject={explaining}
        onOpenChange={(open) => !open && setExplaining(null)}
      />
    </div>
  )
}
