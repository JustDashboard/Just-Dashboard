"use client"

import { memo, useMemo, useState } from "react"
import { Plus } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { VerbActions, type Verb } from "@/components/verbs"
import { DataGrid, type GridLayout } from "@/components/database/grid"
import { updateDocuments, type MongoTarget } from "@/components/database/mongo/api"
import {
  fromJson,
  idLabel,
  parseDocument,
  printJson,
  toJson,
  type Json,
} from "@/components/database/mongo/bson"
import { BsonTree } from "@/components/database/mongo/bson-tree"
import {
  NO_EDITS,
  buildUpdate,
  countEdits,
  editSummary,
  withEdit,
  withoutEdit,
  type Edits,
} from "@/components/database/mongo/changes"
import { cellFilter, tableModel, type Listed } from "@/components/database/mongo/documents/table"
import { valueClass } from "@/components/database/mongo/kinds"
import type { MongoDoc } from "@/components/database/mongo/types"

export type { Listed }

export function useListed(documents: readonly MongoDoc[]): Listed[] {
  return useMemo(
    () =>
      documents.map((doc) => {
        try {
          return { doc, root: parseDocument(doc.canonical) }
        } catch {
          return { doc, root: null }
        }
      }),
    [documents],
  )
}

const hasEdits = (edits: Edits | undefined): edits is Edits =>
  edits !== undefined && Object.keys(edits).length > 0

/* -------------------------------------------------------------------- list */

/**
 * The documents as a list: each one a tree of its fields, typed, with its
 * verbs beside it. Where the role and the collection allow, a value is
 * changed where it stands, and the document then carries its staged edits —
 * and the update they are — until it is sent or let go.
 */
export function ListView({
  target,
  listed,
  expanded,
  editable,
  edits,
  onEdits,
  verbsFor,
  onUpdated,
}: {
  target: MongoTarget
  listed: Listed[]
  /** Every nested document and list open, rather than folded behind its size. */
  expanded: boolean
  /** Fields can be edited in place: the role may write, and this is not a view. */
  editable: boolean
  /** Staged edits by document `_id` (canonical text). */
  edits: Readonly<Record<string, Edits>>
  onEdits: (id: string, edits: Edits) => void
  verbsFor: (doc: MongoDoc) => Verb[]
  onUpdated: () => void
}) {
  return (
    <ol aria-label="Documents" className="divide-y divide-hairline">
      {listed.map((entry, index) => (
        <DocumentBlock
          // The fold is a tree's own state: a change of "expand all" starts the trees again.
          key={`${entry.doc.id || `at:${index}`}:${expanded}`}
          target={target}
          entry={entry}
          expanded={expanded}
          // A document whose _id the projection left out cannot be named in an update.
          editable={editable && entry.doc.id !== ""}
          edits={edits[entry.doc.id] ?? NO_EDITS}
          onEdits={onEdits}
          verbs={verbsFor(entry.doc)}
          onUpdated={onUpdated}
        />
      ))}
    </ol>
  )
}

const DocumentBlock = memo(function DocumentBlock({
  target,
  entry,
  expanded,
  editable,
  edits,
  onEdits,
  verbs,
  onUpdated,
}: {
  target: MongoTarget
  entry: Listed
  expanded: boolean
  editable: boolean
  edits: Edits
  onEdits: (id: string, edits: Edits) => void
  verbs: Verb[]
  onUpdated: () => void
}) {
  const { doc, root } = entry
  const [adding, setAdding] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const staged = hasEdits(edits)
  const update = root && staged ? buildUpdate(doc.id, root, edits) : null

  const send = async () => {
    if (!update) return
    setBusy(true)
    setRefused("")
    try {
      const result = await updateDocuments(target, {
        filter: update.filter,
        update: update.update,
        many: false,
      })
      if (result.matched === 0) {
        // The filter holds the document to what was read. Matching nothing
        // means it is no longer that: changed by somebody else, or deleted.
        setRefused(
          "Nothing was written: the document is no longer what was read here. Somebody else changed these fields, or deleted it. Refresh to see it as it is now.",
        )
        return
      }
      onEdits(doc.id, NO_EDITS)
      if (result.modified === 0) notify.info("No changes: the document already read that way")
      else notify.success("Document updated")
      onUpdated()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <li
      data-slot="mongo-document"
      data-id={doc.id}
      className="group @container min-w-0 px-3 py-2 [contain-intrinsic-size:auto_12rem] [content-visibility:auto]"
    >
      {/* Beside the fields where there is room; over them on a narrow pane,
          where a column of controls would leave the values a few characters. */}
      <div className="flex min-w-0 flex-col-reverse gap-1 @lg:flex-row @lg:items-start @lg:gap-2">
        {root ? (
          <BsonTree
            root={root}
            expand={expanded ? 8 : 0}
            adding={adding}
            onAdding={setAdding}
            className="min-w-0 @lg:flex-1"
            editing={
              editable
                ? {
                    edits,
                    onEdit: (edit) => {
                      setRefused("")
                      onEdits(doc.id, withEdit(edits, edit))
                    },
                    onRevert: (path) => {
                      setRefused("")
                      onEdits(doc.id, withoutEdit(edits, path))
                    },
                  }
                : undefined
            }
          />
        ) : (
          <pre className="min-w-0 font-mono text-xs break-all whitespace-pre-wrap @lg:flex-1">
            {doc.canonical}
          </pre>
        )}
        <VerbActions
          className="@max-lg:self-end"
          verbs={
            editable && root?.type === "object"
              ? withBeforeDanger(verbs, {
                  key: "add-field",
                  label: "Add a field",
                  icon: Plus,
                  run: () => setAdding(true),
                })
              : verbs
          }
          dim
          menuLabel={`More actions for ${idLabel(doc.id)}`}
        />
      </div>
      {staged && root && (
        <div
          data-slot="mongo-pending"
          className="mt-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 rounded-md bg-surface-sunken px-2.5 py-1.5"
        >
          <span className="shrink-0 text-xs font-medium">
            {editSummary(countEdits(edits, root))}
          </span>
          <code
            className="min-w-0 flex-1 basis-48 truncate font-mono text-hint text-muted-foreground"
            title={update?.statement}
          >
            {update ? update.statement : "This edit cannot be sent in place"}
          </code>
          <div className="flex shrink-0 items-center gap-1.5">
            <Button
              size="xs"
              variant="ghost"
              disabled={busy}
              onClick={() => {
                setRefused("")
                onEdits(doc.id, NO_EDITS)
              }}
            >
              Discard
            </Button>
            <Button size="xs" pending={busy} disabled={!update} onClick={() => void send()}>
              Update
            </Button>
          </div>
          {refused && (
            <p role="alert" className="basis-full text-hint leading-relaxed text-destructive">
              {refused}
            </p>
          )}
        </div>
      )}
    </li>
  )
})

/** The verbs with one more, placed before the first that destroys. */
function withBeforeDanger(verbs: Verb[], extra: Verb): Verb[] {
  const at = verbs.findIndex((verb) => verb.danger)
  return at < 0 ? [...verbs, extra] : [...verbs.slice(0, at), extra, ...verbs.slice(at)]
}

/* -------------------------------------------------------------------- JSON */

/**
 * The documents as canonical Extended JSON: the text an export writes and the
 * editor loads, every type spelled out. Typed values take their kind's hue.
 */
export function JsonView({
  listed,
  verbsFor,
}: {
  listed: Listed[]
  verbsFor: (doc: MongoDoc) => Verb[]
}) {
  return (
    <ol aria-label="Documents" className="divide-y divide-hairline">
      {listed.map(({ doc, root }, index) => (
        <li
          key={doc.id || `at:${index}`}
          data-slot="mongo-document"
          className="group @container min-w-0 px-3 py-2 [contain-intrinsic-size:auto_14rem] [content-visibility:auto]"
        >
          <div className="flex min-w-0 flex-col-reverse gap-1 @lg:flex-row @lg:items-start @lg:gap-2">
            <pre className="min-w-0 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap @lg:flex-1">
              {root ? <JsonText json={toJson(root)} depth={0} /> : doc.canonical}
            </pre>
            <VerbActions
              className="@max-lg:self-end"
              verbs={verbsFor(doc)}
              dim
              menuLabel={`More actions for ${idLabel(doc.id)}`}
            />
          </div>
        </li>
      ))}
    </ol>
  )
}

/** Extended JSON text, indented, with a typed value on one line in its hue. */
function JsonText({ json, depth }: { json: Json; depth: number }): React.ReactNode {
  const pad = "  ".repeat(depth + 1)
  const close = "  ".repeat(depth)
  const punctuation = "text-muted-foreground/60"
  if (json.k === "object") {
    // A wrapper — {"$oid": "…"} — is one value, not a document to open out.
    const typed = fromJson(json)
    if (typed.type !== "object" && typed.type !== "array") {
      return <span className={valueClass(typed.type) || undefined}>{printJson(json)}</span>
    }
    if (json.entries.length === 0) return <span className={punctuation}>{"{}"}</span>
    return (
      <>
        <span className={punctuation}>{"{"}</span>
        {json.entries.map(([key, value], index) => (
          <span key={`${key}:${index}`}>
            {"\n"}
            {pad}
            <span className="text-muted-foreground">{JSON.stringify(key)}</span>
            <span className={punctuation}>: </span>
            <JsonText json={value} depth={depth + 1} />
            {index < json.entries.length - 1 && <span className={punctuation}>,</span>}
          </span>
        ))}
        {"\n"}
        {close}
        <span className={punctuation}>{"}"}</span>
      </>
    )
  }
  if (json.k === "array") {
    if (json.items.length === 0) return <span className={punctuation}>[]</span>
    return (
      <>
        <span className={punctuation}>[</span>
        {json.items.map((item, index) => (
          <span key={index}>
            {"\n"}
            {pad}
            <JsonText json={item} depth={depth + 1} />
            {index < json.items.length - 1 && <span className={punctuation}>,</span>}
          </span>
        ))}
        {"\n"}
        {close}
        <span className={punctuation}>]</span>
      </>
    )
  }
  if (json.k === "string") return <span>{JSON.stringify(json.value)}</span>
  if (json.k === "number") return <span className="numeric">{json.raw}</span>
  if (json.k === "bool") {
    return <span className={valueClass("bool")}>{json.value ? "true" : "false"}</span>
  }
  return <span className={valueClass("null")}>null</span>
}

/* ------------------------------------------------------------------- table */

/**
 * The documents as a table: the read-only grid over the union of their
 * top-level fields. A nested document or a list is its JSON on one line; the
 * row opens the whole document.
 */
export function TableView({
  label,
  listed,
  layout,
  onLayoutChange,
  offset,
  loading,
  onOpen,
  onFilter,
  footer,
  className,
}: {
  label: string
  listed: Listed[]
  layout: GridLayout
  onLayoutChange: (layout: GridLayout) => void
  /** Where the page starts, so its first row is numbered as the query numbers it. */
  offset: number
  loading: boolean
  onOpen?: (doc: MongoDoc) => void
  /** Hands over the clause for "filter by this value". */
  onFilter?: (clause: string) => void
  /** The page's own controls, at the right of the grid's status strip. */
  footer?: React.ReactNode
  className?: string
}) {
  const model = useMemo(() => tableModel(listed), [listed])
  return (
    <DataGrid
      label={label}
      columns={model.columns}
      rows={model.rows}
      sortMode="client"
      findable
      selectable={false}
      layout={layout}
      onLayoutChange={onLayoutChange}
      rowNumberOffset={offset}
      loading={loading}
      footer={footer}
      onOpenRow={onOpen ? (row) => onOpen(listed[row.index].doc) : undefined}
      onFilter={
        onFilter
          ? (request) => {
              const clause = cellFilter(request, model)
              if (clause) onFilter(clause)
              else notify.info("That field cannot be named in a filter from here")
            }
          : undefined
      }
      className={cn("[--jd-grid-ground:var(--card)]", className)}
    />
  )
}
