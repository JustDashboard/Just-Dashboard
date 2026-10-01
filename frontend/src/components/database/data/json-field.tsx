"use client"

import { useMemo, useRef, useState } from "react"
import {
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  Cross,
  Plus,
  Route,
  Trash,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import { RowActions } from "@/components/icon-action"
import { Input } from "@/components/ui/input"
import { VALUE_KIND_CLASS, jsonPath } from "@/components/database/kit"
import {
  addChild,
  parseJsonDoc,
  removeChild,
  replaceNode,
  scalarLiteral,
  scalarText,
  sizeOf,
  type JsonNode,
} from "@/components/database/data/json-doc"

/** How many entries of one object or array are drawn before the rest are asked for. */
const PAGE = 100

/** The most characters of one string drawn in a row. */
const CLAMP = 240

type Segment = string | number

const HUE: Record<JsonNode["kind"], string> = {
  string: VALUE_KIND_CLASS.string,
  number: VALUE_KIND_CLASS.number,
  boolean: VALUE_KIND_CLASS.boolean,
  null: VALUE_KIND_CLASS.null,
  object: "",
  array: "",
}

const ACTION =
  "flex size-5 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground"

/**
 * A JSON field's document as a tree — read one level at a time and, when
 * `onChange` is given, edited where it stands: press a value to type another,
 * add a key to an object or an item to an array, take one out.
 *
 * It is drawn like the section's `JsonTree` and differs from it in what it is
 * a tree *of*: the document's own text, not the values JavaScript makes of
 * it. A number is printed with the digits it was stored with, and an edit
 * hands back the same text with one node rewritten, so nothing the reader did
 * not touch is changed on its way back to the database.
 */
export function JsonDocTree({
  text,
  label,
  onChange,
  expand = 2,
  className,
}: {
  /** The document, as text. */
  text: string
  /** What the document is: the column's name, for the names of its controls. */
  label: string
  /** The document with one change made. Absent = the tree is read, not edited. */
  onChange?: (text: string) => void
  /** How many levels are open to begin with. */
  expand?: number
  className?: string
}) {
  const doc = useMemo(() => parseJsonDoc(text), [text])
  if (!doc) {
    // Not JSON: a text column typed as one, or a value the engine let in.
    return (
      <pre
        className={cn(
          "max-h-64 overflow-auto font-mono text-xs leading-relaxed whitespace-pre-wrap",
          className,
        )}
      >
        {text}
      </pre>
    )
  }
  return (
    <div
      data-slot="json-doc-tree"
      className={cn("min-w-0 font-mono text-xs leading-relaxed", className)}
    >
      <Node
        node={doc}
        text={text}
        label={label}
        path={[]}
        depth={0}
        expand={expand}
        onChange={onChange}
      />
    </div>
  )
}

function Node({
  node,
  name,
  text,
  label,
  path,
  depth,
  expand,
  parent,
  index,
  onChange,
}: {
  node: JsonNode
  /** The key or index this value sits under; absent for the document itself. */
  name?: Segment
  text: string
  label: string
  path: Segment[]
  depth: number
  expand: number
  /** The container this node is a child of, and where in it. */
  parent?: JsonNode
  index?: number
  onChange?: (text: string) => void
}) {
  const container = node.kind === "object" || node.kind === "array"
  const [open, setOpen] = useState(depth < expand)
  const [shown, setShown] = useState(PAGE)
  const [editing, setEditing] = useState(false)
  const [adding, setAdding] = useState(false)
  const [error, setError] = useState<string>()
  const what = name === undefined ? label : String(name)
  const indent = depth * 14 + 2

  const children: { key: string; name: Segment; node: JsonNode }[] =
    node.kind === "object"
      ? node.entries.map((entry, at) => ({
          // Two keys of one name are two rows.
          key:
            node.entries.findIndex((other) => other.key === entry.key) === at
              ? entry.key
              : `${entry.key}\u0000${at}`,
          name: entry.key,
          node: entry.value,
        }))
      : node.kind === "array"
        ? node.items.map((item, at) => ({ key: String(at), name: at, node: item }))
        : []

  const settle = (typed: string) => {
    if (!onChange) return
    if (typed === scalarText(node, text)) {
      setEditing(false)
      setError(undefined)
      return
    }
    const read = scalarLiteral(typed)
    if (!read.ok) {
      setError(read.error)
      return
    }
    setEditing(false)
    setError(undefined)
    onChange(replaceNode(text, node, read.literal))
  }

  return (
    <div>
      <div
        className="group flex min-h-6 min-w-0 items-center gap-1 rounded-sm pr-1 hover:bg-row-hover"
        style={{ paddingLeft: `${indent}px` }}
      >
        {container ? (
          <button
            type="button"
            // A disclosure, not a tree widget: nothing here is selected, and
            // every row's controls are reached with Tab as they are drawn.
            aria-expanded={open}
            aria-label={`${open ? "Collapse" : "Expand"} ${what}`}
            onClick={() => setOpen(!open)}
            className="flex size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground"
          >
            {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
          </button>
        ) : (
          <span className="size-4 shrink-0" />
        )}
        {name !== undefined && (
          <span className="max-w-[45%] shrink-0 truncate text-muted-foreground">
            {name}
            <span className="text-muted-foreground/50">:</span>
          </span>
        )}
        {container ? (
          <span className="min-w-0 truncate text-muted-foreground/70">
            {node.kind === "array" ? "[ ]" : "{ }"} {sizeOf(node)}
          </span>
        ) : editing ? (
          <ScalarInput
            initial={scalarText(node, text)}
            label={`Value of ${what}`}
            invalid={error !== undefined}
            onSettle={settle}
            onCancel={() => {
              setEditing(false)
              setError(undefined)
            }}
          />
        ) : onChange ? (
          <button
            type="button"
            aria-label={`Edit ${what}`}
            onClick={() => setEditing(true)}
            className={cn(
              "-mx-1 min-w-0 truncate rounded-sm px-1 text-left focus-ring transition-colors hover:bg-control-hover",
              HUE[node.kind],
            )}
          >
            <Scalar node={node} text={text} />
          </button>
        ) : (
          <span className={cn("min-w-0 truncate", HUE[node.kind])}>
            <Scalar node={node} text={text} />
          </span>
        )}
        {!editing && (
          <RowActions className="ml-auto pl-2">
            <button
              type="button"
              aria-label={`Copy the path to ${what}`}
              title="Copy path"
              onClick={() => void copyText(jsonPath(path), "Path copied")}
              className={ACTION}
            >
              <Route className="size-3" />
            </button>
            <button
              type="button"
              aria-label={`Copy the value of ${what}`}
              title="Copy value"
              onClick={() =>
                void copyText(
                  node.kind === "string" ? node.value : text.slice(node.start, node.end),
                  "Value copied",
                )
              }
              className={ACTION}
            >
              <Copy className="size-3" />
            </button>
            {onChange && container && (
              <button
                type="button"
                aria-label={
                  node.kind === "object" ? `Add a key to ${what}` : `Add an item to ${what}`
                }
                title={node.kind === "object" ? "Add a key" : "Add an item"}
                onClick={() => {
                  setOpen(true)
                  setAdding(true)
                }}
                className={ACTION}
              >
                <Plus className="size-3" />
              </button>
            )}
            {onChange && parent && index !== undefined && (
              <button
                type="button"
                aria-label={`Remove ${what}`}
                title="Remove"
                onClick={() => onChange(removeChild(text, parent, index))}
                className={ACTION}
              >
                <Trash className="size-3" />
              </button>
            )}
          </RowActions>
        )}
      </div>
      {error && (
        <p
          role="alert"
          className="font-sans text-hint text-destructive"
          style={{ paddingLeft: `${indent + 20}px` }}
        >
          {error}
        </p>
      )}
      {container && open && (
        <div>
          {children.slice(0, shown).map((child, at) => (
            <Node
              key={child.key}
              node={child.node}
              name={child.name}
              text={text}
              label={label}
              path={[...path, child.name]}
              depth={depth + 1}
              expand={expand}
              parent={node}
              index={at}
              onChange={onChange}
            />
          ))}
          {children.length > shown && (
            <button
              type="button"
              onClick={() => setShown(shown + PAGE)}
              className="rounded-sm py-0.5 font-sans text-hint text-muted-foreground focus-ring hover:text-foreground"
              style={{ marginLeft: `${indent + 34}px` }}
            >
              Show {Math.min(PAGE, children.length - shown).toLocaleString()} more of{" "}
              {children.length.toLocaleString()}
            </button>
          )}
          {adding && onChange && (
            <AddRow
              keyed={node.kind === "object"}
              indent={indent + 14}
              onAdd={(literal, key) => {
                const added = addChild(text, node, literal, key)
                if (!added.ok) return added.error
                setAdding(false)
                // The new entry is the last: it is drawn, however long the level is.
                setShown(Math.max(shown, children.length + 1))
                onChange(added.text)
                return undefined
              }}
              onCancel={() => setAdding(false)}
            />
          )}
        </div>
      )}
    </div>
  )
}

/** A scalar as its row prints it: a string in its quotes and cut, a number digit for digit. */
function Scalar({ node, text }: { node: JsonNode; text: string }) {
  if (node.kind === "string") {
    return JSON.stringify(node.value.length > CLAMP ? `${node.value.slice(0, CLAMP)}…` : node.value)
  }
  return text.slice(node.start, node.end)
}

const FIELD = "h-6 min-w-0 flex-1 rounded-sm px-1 py-0 font-mono sm:h-5 sm:text-xs"

/** One value being typed: Enter or leaving the field settles it, Escape puts it back. */
function ScalarInput({
  initial,
  label,
  invalid,
  onSettle,
  onCancel,
}: {
  initial: string
  label: string
  invalid: boolean
  onSettle: (typed: string) => void
  onCancel: () => void
}) {
  const [draft, setDraft] = useState(initial)
  // Enter settles and takes the field away, and a field taken away loses the
  // focus: the same text is handed over once, not once for each.
  const handed = useRef<string | null>(null)
  const settle = () => {
    if (handed.current === draft) return
    handed.current = draft
    onSettle(draft)
  }
  return (
    <Input
      autoFocus
      value={draft}
      aria-label={label}
      aria-invalid={invalid || undefined}
      spellCheck={false}
      autoComplete="off"
      className={FIELD}
      onFocus={(event) => event.currentTarget.select()}
      onChange={(event) => setDraft(event.target.value)}
      onBlur={settle}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          event.preventDefault()
          settle()
        } else if (event.key === "Escape") {
          // The field's, not the panel's: Escape here only gives the edit up.
          event.stopPropagation()
          onCancel()
        }
      }}
    />
  )
}

/** The row a new key or item is typed in, at the end of its container. */
function AddRow({
  keyed,
  indent,
  onAdd,
  onCancel,
}: {
  /** The container is an object: the value needs a key. */
  keyed: boolean
  indent: number
  /** Adds the entry, or answers why it cannot be added. */
  onAdd: (literal: string, key?: string) => string | undefined
  onCancel: () => void
}) {
  const [key, setKey] = useState("")
  const [value, setValue] = useState("")
  const [error, setError] = useState<string>()
  const submit = () => {
    if (keyed && key === "") {
      setError("A key needs a name")
      return
    }
    const read = scalarLiteral(value)
    if (!read.ok) {
      setError(read.error)
      return
    }
    setError(onAdd(read.literal, keyed ? key : undefined))
  }
  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key !== "Escape") return
    event.stopPropagation()
    onCancel()
  }
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault()
        submit()
      }}
    >
      <div
        className="flex min-h-6 min-w-0 items-center gap-1 pr-1"
        style={{ paddingLeft: `${indent + 20}px` }}
      >
        {keyed && (
          <Input
            autoFocus
            value={key}
            aria-label="New key"
            placeholder="key"
            spellCheck={false}
            autoComplete="off"
            className={cn(FIELD, "max-w-28")}
            onChange={(event) => setKey(event.target.value)}
            onKeyDown={onKeyDown}
          />
        )}
        <Input
          autoFocus={!keyed}
          value={value}
          aria-label="New value"
          placeholder="value"
          spellCheck={false}
          autoComplete="off"
          className={FIELD}
          onChange={(event) => setValue(event.target.value)}
          onKeyDown={onKeyDown}
        />
        <button type="submit" aria-label="Add" title="Add" className={ACTION}>
          <Check className="size-3" />
        </button>
        <button
          type="button"
          aria-label="Cancel"
          title="Cancel"
          className={ACTION}
          onClick={onCancel}
        >
          <Cross className="size-3" />
        </button>
      </div>
      {error && (
        <p
          role="alert"
          className="font-sans text-hint text-destructive"
          style={{ paddingLeft: `${indent + 20}px` }}
        >
          {error}
        </p>
      )}
    </form>
  )
}
