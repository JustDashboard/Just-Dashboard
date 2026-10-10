"use client"

import { useState } from "react"
import {
  ChevronDown,
  ChevronRight,
  Copy,
  FloppyDisk,
  Pencil,
  Plus,
  Route,
  Trash,
} from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { CodeEditor } from "@/components/code-editor"
import { Field, FormFact } from "@/components/form"
import { IconAction, RowActions } from "@/components/icon-action"
import { PaneFooter } from "@/components/panel"
import { EmptyState, LoadingRows } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { EngineMark, VALUE_KIND_CLASS, jsonPath } from "@/components/database/kit"
import { redisDelete, redisJson, redisWrite } from "@/components/database/redis/api"
import { bytesId, bytesLabel } from "@/components/database/redis/bytes"
import { EditorStrip } from "@/components/database/redis/keys/editor-parts"
import {
  definitePath,
  nodeSize,
  parseJsonDoc,
  printJsonDoc,
  type JsonNode,
} from "@/components/database/redis/keys/json-doc"
import type { KeyEditorProps } from "@/components/database/redis/keys/key-pane"
import { MemberDialog } from "@/components/database/redis/keys/member-dialog"
import { ReadError } from "@/components/database/redis/read-error"

/** How many entries of one object or array are drawn before the rest are asked for. */
const PAGE = 100
const CLAMP = 240

type Segment = string | number

/** A change being typed: a value replaced at a path, or one added under it. */
type Draft = {
  path: string
  /** Adding to an object asks for the new key; to an array, nothing more. */
  adding?: "object" | "array"
  key: string
  text: string
  /** The array being added to, so the new item is written after its last. */
  into?: JsonNode
}

/**
 * A JSON document: a tree to read and change a place at a time, or the whole
 * text.
 *
 * Every change is written to the path it is made at, so editing one field of
 * a large document sends that field and not the document. The document is
 * read and written as text — a number keeps every digit it was stored with,
 * and the keys keep their order. A path can narrow what is read; where it
 * matches more than one place there is no single address to write to, and
 * the matches are shown to read.
 */
export function JsonEditor({ redis, name, epoch, onChanged, confirm }: KeyEditorProps) {
  const { target, db, engine, canWrite, canDestroy } = redis
  const [view, setView] = useState<"tree" | "text">("tree")
  const [path, setPath] = useState("$")
  const [typedPath, setTypedPath] = useState("$")
  const read = usePoll((signal) => redisJson(target, name, path, signal), 0, [
    target.id,
    target.db,
    bytesId(name),
    path,
    epoch,
  ])
  const [draft, setDraft] = useState<Draft | null>(null)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const [text, setText] = useState<string | null>(null)

  const matches = read.data?.matches
  const json = read.data?.info.json
  const single = definitePath(path) && matches?.length === 1
  const root = single ? matches[0] : undefined
  const editable = canWrite && single
  const whole = root ? printJsonDoc(root, 2) : ""
  const shownText = text ?? whole
  const dirty = text !== null && text !== whole

  const written = () => {
    read.refresh()
    onChanged()
  }

  const begin = (next: Draft) => {
    setDraft(next)
    setRefused("")
    setOpen(true)
  }

  const typed = draft ? parseJsonDoc(draft.text) : null

  const save = async () => {
    if (!draft || !typed) return
    setBusy(true)
    setRefused("")
    try {
      if (draft.adding === "array" && draft.into?.kind === "array") {
        // RedisJSON has no "set past the end": the array is written back with
        // the new item after its last.
        await redisWrite(target, {
          key: name,
          type: "json",
          path: draft.path,
          value: printJsonDoc({ kind: "array", items: [...draft.into.items, typed] }),
        })
      } else {
        await redisWrite(target, {
          key: name,
          type: "json",
          path:
            draft.adding === "object"
              ? `${draft.path}${jsonPath([draft.key]).slice(1)}`
              : draft.path,
          value: draft.text,
        })
      }
      setOpen(false)
      setDraft(null)
      written()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const saveText = async () => {
    if (!dirty || busy) return
    if (!parseJsonDoc(shownText)) {
      notify.error("Not saved", undefined, { description: "The text is not valid JSON." })
      return
    }
    setBusy(true)
    try {
      await redisWrite(target, { key: name, type: "json", path, value: shownText })
      setText(null)
      written()
      notify.success("Document saved")
    } catch (err) {
      notify.error("Could not save the document", err)
    } finally {
      setBusy(false)
    }
  }

  const remove = (at: string) =>
    confirm({
      title: "Remove from document",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{at}</span>,
        facts: (
          <>
            <FormFact label="Of" mono>
              {bytesLabel(name)}
            </FormFact>
            <FormFact label="In">db{db}</FormFact>
          </>
        ),
      },
      description: "What is at this path is removed from the document, with everything under it.",
      confirmLabel: "Remove",
      action: async () => {
        await redisDelete(target, { key: name, path: at })
      },
      onDone: written,
    })

  const actions: NodeActions | undefined = editable
    ? {
        edit: (at, node) => begin({ path: at, key: "", text: printJsonDoc(node, 2) }),
        add: (at, node) =>
          begin({
            path: at,
            adding: node.kind === "array" ? "array" : "object",
            key: "",
            text: "",
            into: node,
          }),
        remove: canDestroy ? remove : undefined,
      }
    : undefined

  const tab = (id: "tree" | "text", label: string) => (
    <button
      type="button"
      aria-pressed={view === id}
      onClick={() => setView(id)}
      className={tabClasses(view === id, "h-10")}
    >
      {label}
    </button>
  )

  return (
    <>
      <EditorStrip className="py-0">
        <div role="group" aria-label="How the document is read" className="-ml-3 flex self-stretch">
          {tab("tree", "Tree")}
          {tab("text", "Text")}
        </div>
        <form
          className="flex items-center gap-1.5"
          onSubmit={(event) => {
            event.preventDefault()
            setText(null)
            setPath(typedPath.trim() || "$")
          }}
        >
          <label htmlFor="redis-json-path" className="text-hint text-muted-foreground">
            Path
          </label>
          <Input
            id="redis-json-path"
            value={typedPath}
            spellCheck={false}
            autoComplete="off"
            onChange={(event) => setTypedPath(event.target.value)}
            className="h-7 w-44 px-2 font-mono text-xs sm:h-7 sm:text-xs"
          />
          {typedPath.trim() !== path && (
            <Button type="submit" size="xs" variant="outline">
              Read
            </Button>
          )}
        </form>
        <span className="min-w-0 flex-1" />
        {dirty && (
          <>
            <span className="text-hint font-medium text-(--git-modified)">Unsaved changes</span>
            <Button size="xs" variant="ghost" disabled={busy} onClick={() => setText(null)}>
              Revert
            </Button>
          </>
        )}
        {root && (
          <Button
            size="xs"
            variant="ghost"
            onClick={() => void copyText(shownText, "Document copied")}
          >
            <Copy />
            Copy
          </Button>
        )}
        {view === "text" && editable && (
          <Button size="xs" pending={busy} disabled={!dirty} onClick={saveText}>
            <FloppyDisk />
            Save
          </Button>
        )}
      </EditorStrip>

      {read.error && !read.data ? (
        <ReadError error={read.error} onRetry={read.refresh} className="m-3" />
      ) : !read.data ? (
        <LoadingRows rows={6} className="p-3" />
      ) : json?.tooLarge ? (
        <EmptyState
          className="min-h-0 flex-1 border-0"
          title={`This document is ${bytes(json.bytes)}`}
          description="It is too large to read whole. Give a path above to read a part of it, such as $.items[0]."
        />
      ) : !matches || matches.length === 0 ? (
        <EmptyState
          className="min-h-0 flex-1 border-0"
          title="Nothing is at that path"
          description={`${path} matches no place in the document.`}
          action={
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                setTypedPath("$")
                setPath("$")
              }}
            >
              Read the whole document
            </Button>
          }
        />
      ) : view === "text" && single ? (
        <CodeEditor
          className="min-h-0 flex-1"
          language="json"
          value={shownText}
          readOnly={!editable}
          wordWrap
          onChange={setText}
          onSave={saveText}
        />
      ) : (
        <div
          data-slot="redis-json-tree"
          className="min-h-0 flex-1 overflow-auto p-2 font-mono text-xs leading-relaxed"
        >
          {single ? (
            <Node node={matches[0]} base={path} path={[]} depth={0} actions={actions} />
          ) : (
            matches.map((match, index) => (
              <div key={index}>
                <p className="px-1 pt-2 pb-1 font-sans text-hint text-muted-foreground">
                  Match {index + 1} of {matches.length}
                </p>
                <Node node={match} base={path} path={[]} depth={0} />
              </div>
            ))
          )}
        </div>
      )}

      <PaneFooter className="gap-x-4 text-hint text-muted-foreground">
        <span className="numeric">{json ? bytes(json.bytes) : ""}</span>
        <span className="min-w-0 flex-1 truncate">
          {matches && !single && matches.length > 0
            ? "A path that matches several places is read, not edited."
            : ""}
        </span>
      </PaneFooter>

      {draft && (
        <MemberDialog
          open={open}
          onOpenChange={setOpen}
          onCancel={() => {
            setOpen(false)
            setDraft(null)
          }}
          title={draft.adding ? "Add to document" : "Edit value"}
          name={name}
          type="ReJSON-RL"
          db={db}
          command={draft.adding ? "Add" : "Save value"}
          onSubmit={save}
          busy={busy}
          error={refused}
          disabled={!typed || (draft.adding === "object" && draft.key === "")}
          size="lg"
        >
          <p className="font-mono text-hint text-muted-foreground">
            {draft.adding === "array" ? `${draft.path} — a new last item` : draft.path}
          </p>
          {draft.adding === "object" && (
            <Field label="Key" htmlFor="redis-json-key">
              <Input
                id="redis-json-key"
                value={draft.key}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                onChange={(event) => setDraft({ ...draft, key: event.target.value })}
              />
            </Field>
          )}
          <Field
            label="Value, as JSON"
            htmlFor="redis-json-value"
            hint='Text goes in quotes: "paid". A number, true, false, null, an object or an array goes as it is.'
            error={draft.text.trim() !== "" && !typed ? "That is not valid JSON." : undefined}
          >
            <Textarea
              id="redis-json-value"
              value={draft.text}
              spellCheck={false}
              className="max-h-96 min-h-28 font-mono text-xs sm:text-xs"
              onChange={(event) => setDraft({ ...draft, text: event.target.value })}
            />
          </Field>
        </MemberDialog>
      )}
    </>
  )
}

type NodeActions = {
  edit: (path: string, node: JsonNode) => void
  add: (path: string, node: JsonNode) => void
  remove?: (path: string) => void
}

const KIND_CLASS: Record<JsonNode["kind"], string> = {
  object: "text-muted-foreground/70",
  array: "text-muted-foreground/70",
  string: VALUE_KIND_CLASS.string,
  number: VALUE_KIND_CLASS.number,
  boolean: VALUE_KIND_CLASS.boolean,
  null: VALUE_KIND_CLASS.null,
}

function leafText(node: JsonNode): string {
  if (node.kind === "string") {
    return JSON.stringify(node.value.length > CLAMP ? `${node.value.slice(0, CLAMP)}…` : node.value)
  }
  return printJsonDoc(node)
}

const rowButton =
  "flex size-5 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground"

function Node({
  name,
  node,
  base,
  path,
  depth,
  actions,
}: {
  /** The key or position this value sits under; absent for the document itself. */
  name?: Segment
  node: JsonNode
  /** The path the document was read at, which every place under it is addressed from. */
  base: string
  path: Segment[]
  depth: number
  actions?: NodeActions
}) {
  const container = node.kind === "object" || node.kind === "array"
  const [open, setOpen] = useState(depth < 2)
  const [shown, setShown] = useState(PAGE)
  const at = `${base}${jsonPath(path).slice(1)}`
  const label = name ?? "the document"
  const children: [Segment, JsonNode][] =
    node.kind === "object"
      ? node.entries.map((entry) => [entry.key, entry.value])
      : node.kind === "array"
        ? node.items.map((item, index) => [index, item])
        : []

  return (
    <div>
      <div
        className="group flex min-h-6 min-w-0 items-center gap-1 rounded-sm pr-1 hover:bg-row-hover"
        style={{ paddingLeft: `${depth * 14 + 2}px` }}
      >
        {container ? (
          <button
            type="button"
            aria-expanded={open}
            aria-label={`${open ? "Collapse" : "Expand"} ${label}`}
            onClick={() => setOpen(!open)}
            className={cn(rowButton, "size-4 shrink-0")}
          >
            {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
          </button>
        ) : (
          <span className="size-4 shrink-0" />
        )}
        {name !== undefined && (
          <span className="shrink-0 text-muted-foreground">
            {name}
            <span className="text-muted-foreground/50">:</span>
          </span>
        )}
        <span className={cn("min-w-0 truncate", KIND_CLASS[node.kind])}>
          {container
            ? `${node.kind === "array" ? "[ ]" : "{ }"} ${nodeSize(node)}`
            : leafText(node)}
        </span>
        <RowActions className="ml-auto pl-2">
          <button
            type="button"
            aria-label={`Copy the path to ${label}`}
            title="Copy path"
            onClick={() => void copyText(at, "Path copied")}
            className={rowButton}
          >
            <Route className="size-3" />
          </button>
          <button
            type="button"
            aria-label={`Copy the value of ${label}`}
            title="Copy value"
            onClick={() =>
              void copyText(
                node.kind === "string" ? node.value : printJsonDoc(node, 2),
                "Value copied",
              )
            }
            className={rowButton}
          >
            <Copy className="size-3" />
          </button>
          {actions && container && (
            <IconAction
              label={`Add to ${label}`}
              className="size-5"
              onClick={() => actions.add(at, node)}
            >
              <Plus className="size-3" />
            </IconAction>
          )}
          {actions && (
            <IconAction
              label={`Edit ${label}`}
              className="size-5"
              onClick={() => actions.edit(at, node)}
            >
              <Pencil className="size-3" />
            </IconAction>
          )}
          {actions?.remove && path.length > 0 && (
            <IconAction
              label={`Remove ${label}`}
              className="size-5"
              onClick={() => actions.remove?.(at)}
            >
              <Trash className="size-3" />
            </IconAction>
          )}
        </RowActions>
      </div>
      {container && open && (
        <div>
          {children.slice(0, shown).map(([key, child], index) => (
            <Node
              // By position: a document may hold the same key twice.
              key={index}
              name={key}
              node={child}
              base={base}
              path={[...path, key]}
              depth={depth + 1}
              actions={actions}
            />
          ))}
          {children.length > shown && (
            <button
              type="button"
              onClick={() => setShown(shown + PAGE)}
              className="rounded-sm py-0.5 font-sans text-hint text-muted-foreground focus-ring hover:text-foreground"
              style={{ marginLeft: `${(depth + 1) * 14 + 22}px` }}
            >
              Show {Math.min(PAGE, children.length - shown).toLocaleString()} more of{" "}
              {children.length.toLocaleString()}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
