"use client"

import { memo, useMemo, useState } from "react"
import { MoreHorizontal, Plus } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { DimActions, IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { VerbMenu, type Verb } from "@/components/verbs"
import { DataGrid, type GridLayout, type GridSort } from "@/components/database/grid"
import { updateDocuments, type MongoTarget } from "@/components/database/mongo/api"
import {
  dotted,
  fromJson,
  idLabel,
  parseDocument,
  printJson,
  toReadableJson,
  type Json,
} from "@/components/database/mongo/bson"
import { BsonTree, FIRST_FIELDS, useTreeRun } from "@/components/database/mongo/bson-tree"
import {
  NO_EDITS,
  buildUpdate,
  countEdits,
  editSummary,
  overtaken,
  staged,
  withoutEdit,
  type Edits,
  type StagedDocuments,
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

/** What a document is keyed by in a list: its `_id`, or its place where a projection left that out. */
const keyOf = (entry: Listed, index: number) => entry.doc.id || `at:${index}`

/**
 * A document's verbs: the daily ones as icons, the rest behind one menu. They
 * are stops for Tab only on the document the keyboard is in, so a page of
 * fifty documents is not two hundred stops between the query and the pager.
 */
function DocumentVerbs({
  verbs,
  subject,
  reach,
  className,
}: {
  verbs: Verb[]
  /** The document, as its `_id` reads. */
  subject: string
  reach: boolean
  className?: string
}) {
  const inline = verbs.filter((verb) => verb.inline)
  const rest = verbs.filter((verb) => !verb.inline)
  const tabIndex = reach ? 0 : -1
  return (
    <DimActions className={className}>
      {inline.map((verb) => (
        <IconAction
          key={verb.key}
          label={verb.label}
          tabIndex={tabIndex}
          disabled={verb.disabled}
          className={cn(verb.danger && "text-destructive")}
          onClick={() => verb.run()}
        >
          <verb.icon />
        </IconAction>
      ))}
      {rest.length > 0 && (
        <VerbMenu
          verbs={rest}
          trigger={
            <Button
              size="icon-sm"
              variant="ghost"
              tabIndex={tabIndex}
              aria-label={`More actions for ${subject}`}
              className="[&_svg:not([class*='size-'])]:size-3.5"
            >
              <MoreHorizontal />
            </Button>
          }
        />
      )}
    </DimActions>
  )
}

/* -------------------------------------------------------------------- list */

/**
 * The documents as a list: each one a tree of its fields, typed, with its
 * verbs beside it. Where the role and the collection allow, a value is
 * changed where it stands, and the document then carries its staged edits —
 * and the update they are — until it is sent or let go.
 *
 * A pane wide enough for two documents side by side draws them so, and three
 * on a very wide one: a document is a narrow column of short lines, and one
 * to a row left most of a wide pane empty with its verbs at the far edge.
 * Read across, then down. The list is one stop for Tab; the arrows walk its
 * rows, and Page Up and Page Down go a document at a time.
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
  edits: StagedDocuments
  onEdits: (id: string, edits: Edits) => void
  verbsFor: (doc: MongoDoc) => Verb[]
  onUpdated: () => void
}) {
  const keys = useMemo(() => listed.map(keyOf), [listed])
  const run = useTreeRun(keys)
  return (
    <div className="@container">
      <ol
        aria-label="Documents"
        // The cells' own right and bottom edges are the rules between them;
        // the last column's falls just outside the list.
        className="-mr-px grid @5xl:grid-cols-2 @min-[96rem]:grid-cols-3"
        onKeyDown={(event) => {
          run.onKeyDown(event)
          if (event.defaultPrevented) return
          if (event.key !== "PageDown" && event.key !== "PageUp") return
          // A document at a time: the first row of the next one, or of the one before.
          const from = event.target instanceof Element ? event.target.closest("li") : null
          const to =
            event.key === "PageDown" ? from?.nextElementSibling : from?.previousElementSibling
          const row = to?.querySelector<HTMLElement>("[data-tree-row]")
          if (!row) return
          event.preventDefault()
          row.focus()
        }}
      >
        {listed.map((entry, index) => (
          <DocumentBlock
            // The fold is a tree's own state: a change of "expand all" starts the trees again.
            key={`${keys[index]}:${expanded}`}
            target={target}
            entry={entry}
            expanded={expanded}
            // A document whose _id the projection left out cannot be named in an update.
            editable={editable && entry.doc.id !== ""}
            edits={edits[entry.doc.id] ?? NO_EDITS}
            onEdits={onEdits}
            verbs={verbsFor(entry.doc)}
            onUpdated={onUpdated}
            active={run.active === keys[index]}
            onEnter={run.enter(keys[index])}
          />
        ))}
      </ol>
    </div>
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
  active,
  onEnter,
}: {
  target: MongoTarget
  entry: Listed
  expanded: boolean
  editable: boolean
  edits: Edits
  onEdits: (id: string, edits: Edits) => void
  verbs: Verb[]
  onUpdated: () => void
  /** This document holds the list's tab stop. */
  active: boolean
  onEnter: () => void
}) {
  const { doc, root } = entry
  const [adding, setAdding] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const pending = hasEdits(edits)
  const update = root && pending ? buildUpdate(doc.id, root, edits) : null
  // Fields somebody else has written to since their edit was staged: the update would match nothing.
  const moved = root && pending ? overtaken(edits, root) : []
  const subject = idLabel(doc.id)

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
      className="group @container min-w-0 border-r border-b border-hairline px-3 py-2 [contain-intrinsic-size:auto_12rem] [content-visibility:auto]"
      onFocusCapture={active ? undefined : onEnter}
    >
      {/* Beside the fields where there is room; over them on a narrow pane,
          where a column of controls would leave the values a few characters.
          The measure is capped, so on a wide pane that draws one document to
          a row the verbs still stand at the end of its lines, not a pane away. */}
      <div className="relative flex max-w-3xl min-w-0 flex-col-reverse gap-1">
        {root ? (
          <BsonTree
            root={root}
            label={`Fields of ${subject}`}
            expand={expanded ? 8 : 0}
            first={expanded ? undefined : FIRST_FIELDS}
            adding={adding}
            onAdding={setAdding}
            tabbable={active}
            // The verbs stand at the end of the first line: it alone gives them room.
            leadClassName="@lg:pr-32"
            className="min-w-0"
            editing={
              editable
                ? {
                    edits,
                    onEdit: (edit) => {
                      setRefused("")
                      onEdits(doc.id, staged(edits, root, edit))
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
          <pre className="min-w-0 font-mono text-xs break-all whitespace-pre-wrap @lg:pr-32">
            {doc.canonical}
          </pre>
        )}
        <DocumentVerbs
          className="@max-lg:self-end @lg:absolute @lg:-top-0.5 @lg:right-0"
          subject={subject}
          reach={active}
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
        />
      </div>
      {pending && root && (
        <div
          data-slot="mongo-pending"
          className="mt-2 flex max-w-3xl min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 rounded-md bg-surface-sunken px-2.5 py-1.5"
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
          {moved.length > 0 && !refused && (
            <p role="status" className="basis-full text-hint leading-relaxed text-warning">
              {moved.map((edit) => dotted(edit.path) ?? "a field").join(", ")}{" "}
              {moved.length === 1 ? "has" : "have"} been written to since{" "}
              {moved.length === 1 ? "this edit was" : "these edits were"} made, so the update would
              match nothing. Undo the edit and make it again on the value as it is now.
            </p>
          )}
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
 * The documents as Extended JSON: the text the editor loads, every type
 * spelled out and a date as its ISO moment. Typed values take their kind's
 * hue.
 */
export function JsonView({
  listed,
  verbsFor,
}: {
  listed: Listed[]
  verbsFor: (doc: MongoDoc) => Verb[]
}) {
  return (
    <div className="@container">
      <ol aria-label="Documents" className="-mr-px grid @5xl:grid-cols-2 @min-[96rem]:grid-cols-3">
        {listed.map(({ doc, root }, index) => (
          <li
            key={doc.id || `at:${index}`}
            data-slot="mongo-document"
            className="group @container min-w-0 border-r border-b border-hairline px-3 py-2 [contain-intrinsic-size:auto_14rem] [content-visibility:auto]"
          >
            <div className="flex max-w-3xl min-w-0 flex-col-reverse gap-1 @lg:flex-row @lg:items-start @lg:gap-2">
              <pre className="min-w-0 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap @lg:flex-1">
                {root ? <JsonText json={toReadableJson(root)} depth={0} /> : doc.canonical}
              </pre>
              <DocumentVerbs
                className="@max-lg:self-end"
                subject={idLabel(doc.id)}
                reach
                verbs={verbsFor(doc)}
              />
            </div>
          </li>
        ))}
      </ol>
    </div>
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
 *
 * A head pressed where the owner takes the order (`onSort`) asks the server
 * for it — the whole match in that order, the same Sort the query bar holds —
 * rather than shuffling the fifty rows that happen to be on screen. Without
 * an owner (a pipeline's result, which is all here) the rows are ordered in
 * place.
 */
export function TableView({
  label,
  listed,
  layout,
  onLayoutChange,
  offset,
  loading,
  sort,
  onSort,
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
  /** The order the query asks for, as the heads show it. */
  sort?: GridSort
  /** Takes the order a head asks for to the server. */
  onSort?: (sort: GridSort) => void
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
      sort={onSort ? sort : undefined}
      onSortChange={onSort}
      sortMode={onSort ? "server" : "client"}
      banner={
        model.absent > 0 ? (
          <p className="border-b border-hairline px-3 py-1 font-sans text-hint text-muted-foreground">
            A NULL in a column headed &ldquo;n of m&rdquo; may be a field the document does not
            have: only that many of these documents have it.
          </p>
        ) : undefined
      }
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
