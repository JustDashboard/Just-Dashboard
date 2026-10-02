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
  Trash,
} from "@/components/icons"
import { bytes } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { FormFact, FormFacts } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
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
import { useUnloadGuard } from "@/components/database/data/guard"
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
import type { Edits } from "@/components/database/mongo/changes"
import { BulkDialog, type BulkMode } from "@/components/database/mongo/documents/bulk"
import {
  EditDocumentDialog,
  InsertDocumentDialog,
  editorText,
} from "@/components/database/mongo/documents/document-editor"
import { QueryBar } from "@/components/database/mongo/documents/query-bar"
import {
  JsonView,
  ListView,
  TableView,
  useListed,
} from "@/components/database/mongo/documents/views"
import { ExplainDialog, type ExplainSubject } from "@/components/database/mongo/explain"
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
 * of history, so Back returns to the one before. A document is edited as the canonical Extended JSON it is stored
 * as — nothing here passes it through a JavaScript number or a plain object,
 * which is how the old editor rewrote a document's types by saving it — or a
 * field at a time, as the update that change is.
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
  const poll = usePoll((signal) => findDocuments(target, spec, false, signal), 0, [id, identity])
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

  const [edits, setEdits] = useState<Readonly<Record<string, Edits>>>({})
  const stagedOn = Object.values(edits).filter((held) => Object.keys(held).length > 0).length
  useUnloadGuard(stagedOn > 0)
  const [leaving, setLeaving] = useState<(() => void) | null>(null)
  /** Runs a step that replaces the documents on screen, after asking when edits are staged on them. */
  const guarded = (step: () => void) => {
    if (stagedOn > 0) setLeaving(() => step)
    else step()
  }

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
    setEdits({})
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
    guarded(() => apply(next))
  }
  const reset = () => {
    setDraft(EMPTY_QUERY)
    guarded(() => apply(EMPTY_QUERY))
  }
  const turn = (to: number) =>
    guarded(() => {
      setEdits({})
      select({ page: to <= 0 ? null : String(to + 1) })
    })
  const narrow = (clause: string) => {
    const next = { ...applied, filter: mergeFilter(applied.filter, clause) }
    setDraft(next)
    guarded(() => apply(next))
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
                  onDone: reread,
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
    {
      key: "copy-query",
      label: "Copy the query",
      icon: Copy,
      run: () => void copyText(findStatement(collection, applied), "Query copied"),
    },
  ]

  /* ------------------------------------------------------------ problems */

  const typed = draftProblems(draft)
  // The server's refusal belongs to the query it was given: once the bar is
  // edited away from that, the sentence is about something else.
  const untouched = QUERY_FIELDS.every((field) => draft[field] === applied[field])
  const refusal = poll.error && untouched ? refusedField(poll.error.message, applied) : null
  const problems = refusal ? { ...typed, [refusal.field]: refusal.message } : typed

  /* --------------------------------------------------------------- foot */

  const filtered = isFiltered(applied)
  const count = counted.data ?? null
  // Where the reader's own Skip puts page one; a page's place is counted from there.
  const base = window_.skip - window_.page * pageSize
  const words = result
    ? rangeWords(result.skip - base, result.returned, count, filtered, window_.cap)
    : ""
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
        onValueChange={(next) =>
          guarded(() => {
            setEdits({})
            setSize(Number(next))
            select({ page: null })
          })
        }
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
          <IconAction
            label="Read the documents again"
            className="size-7"
            onClick={() =>
              guarded(() => {
                setEdits({})
                reread()
              })
            }
          >
            <RefreshClockwise />
          </IconAction>
          <VerbMenu verbs={pageVerbs} label={`More actions for ${collection}`} />
        </div>
      </WorkbenchHead>

      <QueryBar
        draft={draft}
        onChange={setDraft}
        problems={problems}
        optionsOpen={optionsOpen}
        onOptionsOpen={setOptionsOpen}
        running={poll.loading}
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
        onPick={(picked) => {
          setDraft(picked)
          find(picked)
        }}
        onClearHistory={() =>
          setHistory((held) =>
            held.filter(
              (entry) => !(entry.database === database && entry.collection === collection),
            ),
          )
        }
      />

      <div className="flex h-9 shrink-0 items-center gap-3 border-b border-hairline px-3">
        <div role="group" aria-label="Document views" className="flex h-full gap-1">
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
        <span className="min-w-0 flex-1" />
        {stagedOn > 0 && (
          <span className="text-hint whitespace-nowrap text-(--git-modified)">
            Edits staged on {stagedOn === 1 ? "one document" : `${stagedOn} documents`}
          </span>
        )}
        {poll.error && result && (
          <span role="status" className="min-w-0 truncate text-hint text-warning">
            The last read failed; these are the documents from before.
          </span>
        )}
      </div>

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
              onOpen={setEditing}
              onFilter={narrow}
              footer={pager}
            />
          </div>
        ) : (
          <div className="absolute inset-0 overflow-auto">
            {poll.error && !result ? (
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
                onEdits={(docId, next) => setEdits((held) => ({ ...held, [docId]: next }))}
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
      <Modal
        open={leaving !== null}
        onOpenChange={(open) => !open && setLeaving(null)}
        size="sm"
        title="Unsent edits"
        description="Fields were edited on this page and the update has not been sent"
        footer={
          <>
            <Button variant="outline" onClick={() => setLeaving(null)}>
              Keep editing
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                const step = leaving
                setLeaving(null)
                setEdits({})
                step?.()
              }}
            >
              Discard and go on
            </Button>
          </>
        }
      >
        <p className="text-body leading-relaxed">
          {stagedOn === 1 ? "One document has" : `${stagedOn} documents have`} edits that were not
          sent. Reading other documents leaves them behind; nothing has been written.
        </p>
      </Modal>
    </div>
  )
}
