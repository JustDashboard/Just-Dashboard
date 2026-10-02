"use client"

import { createContext, useCallback, useContext, useId, useMemo, useRef, useState } from "react"
import {
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  Cross,
  Pencil,
  Plus,
  RotateCounterClockwise,
  Route,
  Trash,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  TYPED_INPUTS,
  TYPE_LABEL,
  dotted,
  inputOf,
  printShell,
  sameValue,
  shellScalar,
  sizeWord,
  typedValue,
  type BsonNode,
  type BsonScalar,
  type InputType,
  type Segment,
} from "@/components/database/mongo/bson"
import {
  addedUnder,
  editAt,
  pathKey,
  type Edit,
  type Edits,
} from "@/components/database/mongo/changes"
import { CodeField } from "@/components/database/mongo/code-field"
import { valueClass } from "@/components/database/mongo/kinds"

/** How many entries of one object or array are drawn before the rest are asked for. */
const PAGE = 100

/** The most characters of one string drawn in a row. */
const CLAMP = 240

/** How many fields of a document a list draws before the rest are asked for. */
export const FIRST_FIELDS = 12

/** What an editable tree is given: the staged edits and the two ways they change. */
export type TreeEditing = {
  edits: Edits
  onEdit: (edit: Edit) => void
  onRevert: (path: Segment[]) => void
}

/* ------------------------------------------------------------- the keyboard */

const ROW = "[data-tree-row]"

/**
 * Moves the keyboard between the rows inside `container` on an arrow, Home
 * or End, and says whether it did. A tree handles its own rows; what it
 * cannot place — Down on its last row — is left to whatever holds a run of
 * trees, which calls this over all of them.
 */
export function moveTreeFocus(container: HTMLElement, event: React.KeyboardEvent): boolean {
  const from = event.target
  if (!(from instanceof HTMLElement) || !from.matches(ROW)) return false
  if (event.altKey || event.ctrlKey || event.metaKey) return false
  const rows = Array.from(container.querySelectorAll<HTMLElement>(ROW))
  const at = rows.indexOf(from)
  const to =
    event.key === "ArrowDown"
      ? rows[at + 1]
      : event.key === "ArrowUp"
        ? rows[at - 1]
        : event.key === "Home"
          ? rows[0]
          : event.key === "End"
            ? rows[rows.length - 1]
            : undefined
  if (!to || to === from) return false
  event.preventDefault()
  to.focus()
  return true
}

/**
 * One tab stop for a run of trees: the documents of a list, the samples
 * beside a stage. Tab enters the run at the tree the keyboard was last in
 * (the first, to begin with) and leaves it again; the arrows walk every row
 * of every tree in between. Without this each of fifty documents is a stop of
 * its own between the query and the page's controls.
 */
export function useTreeRun(keys: readonly string[]) {
  const [held, setHeld] = useState<string>()
  const active = held !== undefined && keys.includes(held) ? held : keys[0]
  return {
    /** The tree that holds the run's tab stop. */
    active,
    /** For the element around the run. */
    onKeyDown: (event: React.KeyboardEvent<HTMLElement>) => {
      if (!event.defaultPrevented) moveTreeFocus(event.currentTarget, event)
    },
    /** For the element around one tree: the keyboard is in it now. */
    enter: (key: string) => () => setHeld(key),
  }
}

type Tree = {
  /** Whether this tree holds a tab stop at all: one tree of a run does. */
  tabbable: boolean
  /** The row that holds it, by its path; `""` is the first row. */
  current: string
  setCurrent: (key: string) => void
  /** Puts the keyboard on the row at a path, or on the nearest row above it when that one is gone. */
  focusRow: (path: readonly Segment[]) => void
  leadClassName?: string
}

const TreeContext = createContext<Tree>({
  tabbable: true,
  current: "",
  setCurrent: () => {},
  focusRow: () => {},
})

/**
 * A document as MongoDB holds it, as a tree you open one level at a time.
 *
 * Every value is drawn with its type: an ObjectId, a date, a 64-bit integer
 * and a Decimal128 are spelled the way the shell spells them and take their
 * kind's hue, so `3` and `Long("3")` are never mistaken for each other. A
 * level that is closed is not rendered; one with thousands of entries draws a
 * hundred and offers the next hundred.
 *
 * Given `editing`, a value can be changed where it stands: its type is
 * chosen, not guessed from what was typed, and the change is staged — drawn
 * in the hue of what it is (changed, added, removed) — until the reader
 * sends the update or lets it go.
 *
 * The tree is one stop for Tab. Inside it the arrows move between rows, Right
 * and Left open and close a level, Enter edits the value, and a row's own
 * controls are reached with Tab from that row and from no other.
 */
export function BsonTree({
  root,
  label = "Document",
  expand = 1,
  first,
  editing,
  adding = false,
  onAdding,
  tabbable = true,
  leadClassName,
  className,
}: {
  root: BsonNode
  /** What the tree is of, for a reader who cannot see it. */
  label?: string
  /** How many levels are open to begin with. */
  expand?: number
  /** How many top-level fields are drawn before the rest are asked for. */
  first?: number
  editing?: TreeEditing
  /** The form for a new top-level field is open; the owner's own control opens it. */
  adding?: boolean
  onAdding?: (adding: boolean) => void
  /** `false` for a tree in a run of them that does not hold the run's tab stop. */
  tabbable?: boolean
  /** For the first row alone: room at its end for what the owner draws over that corner. */
  leadClassName?: string
  className?: string
}) {
  const ground = useRef<HTMLDivElement>(null)
  const [current, setCurrent] = useState("")
  const focusRow = useCallback((path: readonly Segment[]) => {
    // After the render that follows: the row a form stood in is drawn again by then.
    requestAnimationFrame(() => {
      const tree = ground.current
      if (!tree) return
      for (let length = path.length; length > 0; length--) {
        const key = CSS.escape(pathKey(path.slice(0, length)))
        const row = tree.querySelector<HTMLElement>(`${ROW}[data-path="${key}"]`)
        if (row) {
          row.focus()
          return
        }
      }
      tree.querySelector<HTMLElement>(ROW)?.focus()
    })
  }, [])
  const tree = useMemo<Tree>(
    () => ({ tabbable, current, setCurrent, focusRow, leadClassName }),
    [tabbable, current, focusRow, leadClassName],
  )
  return (
    <TreeContext.Provider value={tree}>
      <div
        ref={ground}
        role="tree"
        aria-label={label}
        data-slot="bson-tree"
        className={cn("min-w-0 font-mono text-xs leading-relaxed", className)}
        onKeyDown={(event) => moveTreeFocus(event.currentTarget, event)}
      >
        {root.type === "object" || root.type === "array" ? (
          <Children
            node={root}
            path={[]}
            depth={0}
            expand={expand}
            first={first}
            editing={editing}
            root={root}
            adding={adding}
            onAdding={onAdding ?? noop}
          />
        ) : (
          <Row value={root} path={[]} depth={0} expand={expand} root={root} leads />
        )}
      </div>
    </TreeContext.Provider>
  )
}

const noop = () => {}

type RowProps = {
  /** The key or index this value sits under; absent for a bare value. */
  name?: Segment
  value: BsonNode
  path: Segment[]
  depth: number
  expand: number
  root: BsonNode
  editing?: TreeEditing
  /** A field that is staged and not yet in the document. */
  added?: boolean
  /** The tree's first row: where its tab stop is until the reader moves it. */
  leads?: boolean
}

const indent = (depth: number) => ({ paddingLeft: `${depth * 14 + 2}px` })

/** A value's staged state, as the words a reader who cannot see the mark hears. */
const STATE_WORD = { changed: ", changed", added: ", added", removed: ", to be removed" } as const

/** A row's value as it is said: what a folded value holds, or the value itself, cut where it runs long. */
function said(node: BsonNode): string {
  if (node.type === "object" || node.type === "array") return sizeWord(node)
  const text = shellScalar(node)
  return text.length > 80 ? `${text.slice(0, 80)}…` : text
}

/** A value's staged state, as the hue of its gutter mark. */
const STATE_MARK = {
  changed: "bg-(--git-modified)",
  added: "bg-(--git-added)",
  removed: "bg-(--git-deleted)",
} as const

function Row({ name, value, path, depth, expand, root, editing, added, leads }: RowProps) {
  const tree = useContext(TreeContext)
  const [open, setOpen] = useState(depth < expand)
  const [form, setForm] = useState(false)
  const [adding, setAdding] = useState(false)
  // The keyboard is on this row or on one of its controls: they are then its to Tab through.
  const [held, setHeld] = useState(false)

  const staged = editing ? editAt(editing.edits, path) : { own: undefined, above: undefined }
  // What is drawn: the staged value where there is one, else the document's.
  const shown = staged.own?.op === "set" ? staged.own.value : value
  const state = added
    ? "added"
    : staged.own?.op === "unset"
      ? "removed"
      : staged.own
        ? "changed"
        : undefined
  const container = shown.type === "object" || shown.type === "array"
  const label = name === undefined ? "the value" : String(name)

  const addressable = dotted(path) !== null
  const isId = path.length === 1 && path[0] === "_id"
  const free = Boolean(editing) && !staged.above && addressable && !isId
  const canEdit = free && state !== "removed"
  const canRemove = free && typeof name === "string" && !state
  // A value that was replaced whole is shown as it will be; its parts are not edited one by one.
  const inner = staged.own || added ? undefined : editing
  // A field can be added to a document inside this one, where an update can name it.
  const canAddInside = free && !state && shown.type === "object"

  const key = pathKey(path)
  const stop = tree.tabbable && (tree.current === key || (tree.current === "" && leads))
  const reach = held && tree.tabbable ? 0 : -1

  if (form && editing) {
    return (
      <ValueForm
        depth={depth}
        name={label}
        initial={shown}
        onCancel={() => {
          setForm(false)
          tree.focusRow(path)
        }}
        onCommit={(_field, next) => {
          setForm(false)
          // Back to what the document holds is no edit at all.
          if (!added && sameValue(next, value)) editing.onRevert(path)
          else editing.onEdit({ op: "set", path, value: next })
          tree.focusRow(path)
        }}
      />
    )
  }

  const close = () => {
    setOpen(false)
    // The tab stop was on a row inside what is closing: it comes up to this one.
    if (tree.current.startsWith(`${key.slice(0, -1)},`)) tree.setCurrent(key)
  }
  const toggle = () => (open ? close() : setOpen(true))
  const keys = (event: React.KeyboardEvent<HTMLDivElement>) => {
    // Only the row's own keys: a press on one of its controls is that control's.
    if (event.target !== event.currentTarget) return
    if (event.altKey || event.ctrlKey || event.metaKey) return
    switch (event.key) {
      case "ArrowRight":
        if (container && !open) setOpen(true)
        else return
        break
      case "ArrowLeft":
        if (container && open) close()
        else if (path.length > 1) tree.focusRow(path.slice(0, -1))
        else return
        break
      case "Enter":
      case "F2":
        if (canEdit) setForm(true)
        else if (container) toggle()
        else return
        break
      case " ":
        if (container) toggle()
        else return
        break
      case "Delete":
        if (canRemove && editing) editing.onEdit({ op: "unset", path })
        else return
        break
      default:
        return
    }
    event.preventDefault()
  }

  return (
    <div role="none">
      <div
        role="treeitem"
        aria-level={depth + 1}
        aria-expanded={container ? open : undefined}
        // Nothing in a document is selected: a row is only where the keyboard is.
        aria-selected={false}
        aria-label={`${label}: ${said(shown)}${state ? STATE_WORD[state] : ""}`}
        tabIndex={stop ? 0 : -1}
        data-tree-row=""
        data-path={key}
        data-state={state}
        className={cn(
          "group/row relative flex min-h-6 min-w-0 items-center gap-1 rounded-sm pr-1 focus-ring-inset hover:bg-row-hover [@media(hover:none)]:min-h-8",
          state === "removed" && "text-muted-foreground line-through decoration-(--git-deleted)",
          leads && tree.leadClassName,
        )}
        style={indent(depth)}
        onKeyDown={keys}
        onFocus={() => {
          setHeld(true)
          if (tree.current !== key) tree.setCurrent(key)
        }}
        onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget)) setHeld(false)
        }}
      >
        {state && (
          <span
            aria-hidden
            className={cn("absolute inset-y-0.5 left-0 w-0.5 rounded-full", STATE_MARK[state])}
          />
        )}
        {container ? (
          <button
            type="button"
            // For the pointer: the keyboard opens and closes a level with the arrows, on the row.
            tabIndex={-1}
            aria-label={`${open ? "Collapse" : "Expand"} ${label}`}
            onClick={toggle}
            className="flex size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:text-foreground"
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
        {container ? (
          <span className="min-w-0 truncate text-muted-foreground/70">
            {shown.type === "array" ? "[ ]" : "{ }"} {sizeWord(shown)}
          </span>
        ) : (
          <BsonValue
            node={shown}
            onDoubleClick={canEdit ? () => setForm(true) : undefined}
            className="min-w-0 truncate"
          />
        )}
        {/* On the row the pointer is over or the keyboard is on — with a
            finger, the row that was tapped, where they are a finger wide. They
            take no room on any other row: a value is not cut short to keep a
            place for controls that are not drawn. */}
        <div className="ml-auto hidden shrink-0 items-center pl-2 group-focus-within/row:flex [@media(hover:hover)]:group-hover/row:flex">
          {state && editing && (
            <TreeAction
              tabIndex={reach}
              label={`Undo the change to ${label}`}
              onClick={() => {
                editing.onRevert(path)
                // A field that was only staged goes with its edit: the row above takes the keyboard.
                tree.focusRow(path)
              }}
            >
              <RotateCounterClockwise className="size-3" />
            </TreeAction>
          )}
          {canEdit && (
            <TreeAction tabIndex={reach} label={`Edit ${label}`} onClick={() => setForm(true)}>
              <Pencil className="size-3" />
            </TreeAction>
          )}
          {canAddInside && (
            <TreeAction
              tabIndex={reach}
              label={`Add a field to ${label}`}
              onClick={() => {
                setOpen(true)
                setAdding(true)
              }}
            >
              <Plus className="size-3" />
            </TreeAction>
          )}
          {canRemove && editing && (
            <TreeAction
              tabIndex={reach}
              label={`Remove ${label}`}
              onClick={() => {
                editing.onEdit({ op: "unset", path })
                // The control leaves with the press; the row it was on stays.
                tree.focusRow(path)
              }}
            >
              <Trash className="size-3" />
            </TreeAction>
          )}
          <TreeAction
            touchless
            tabIndex={reach}
            label={`Copy the path to ${label}`}
            onClick={() => void copyText(dotted(path) ?? JSON.stringify(path), "Path copied")}
          >
            <Route className="size-3" />
          </TreeAction>
          <TreeAction
            touchless
            tabIndex={reach}
            label={`Copy the value of ${label}`}
            onClick={() =>
              void copyText(
                shown.type === "string" ? shown.text : printShell(shown, true),
                "Value copied",
              )
            }
          >
            <Copy className="size-3" />
          </TreeAction>
        </div>
      </div>
      {container && open && (
        <Children
          node={shown}
          path={path}
          depth={depth + 1}
          expand={expand}
          root={root}
          editing={state === "removed" ? undefined : inner}
          adding={adding}
          onAdding={setAdding}
        />
      )}
    </div>
  )
}

function Children({
  node,
  path,
  depth,
  expand,
  root,
  first,
  editing,
  adding,
  onAdding,
}: {
  node: Extract<BsonNode, { type: "object" | "array" }>
  path: Segment[]
  depth: number
  expand: number
  root: BsonNode
  /** How many entries are drawn to begin with; a hundred where it is not said. */
  first?: number
  editing?: TreeEditing
  /** The form for a new field is open at the end of this level. */
  adding: boolean
  onAdding: (adding: boolean) => void
}) {
  const tree = useContext(TreeContext)
  const [shown, setShown] = useState(first ?? PAGE)
  const entries: [Segment, BsonNode][] =
    node.type === "array"
      ? node.items.map((item, index) => [index, item])
      : node.fields.map((entry) => [entry.name, entry.value])
  const staged = editing ? addedUnder(editing.edits, root, path) : []
  // A field can be added to a document the update can name, and to the root.
  const canAdd =
    Boolean(editing) &&
    node.type === "object" &&
    (path.length === 0 || dotted(path) !== null) &&
    !editAt(editing!.edits, path).above
  const names = new Set(node.type === "object" ? node.fields.map((entry) => entry.name) : [])
  for (const edit of staged) names.add(String(edit.path[edit.path.length - 1]))

  return (
    <div role={depth === 0 ? "none" : "group"}>
      {entries.slice(0, shown).map(([key, child], index) => (
        <Row
          key={key}
          name={key}
          value={child}
          path={[...path, key]}
          depth={depth}
          expand={expand}
          root={root}
          editing={editing}
          leads={depth === 0 && index === 0}
        />
      ))}
      {entries.length > shown && (
        <button
          type="button"
          tabIndex={tree.tabbable ? 0 : -1}
          onClick={() => setShown(shown + PAGE)}
          className="rounded-sm py-0.5 text-hint text-muted-foreground focus-ring hover:text-foreground"
          style={{ marginLeft: `${depth * 14 + 22}px` }}
        >
          Show {Math.min(PAGE, entries.length - shown).toLocaleString("en-US")} more of{" "}
          {entries.length.toLocaleString("en-US")}
        </button>
      )}
      {staged.map((edit) =>
        edit.op === "set" ? (
          <Row
            key={`added:${String(edit.path[edit.path.length - 1])}`}
            name={edit.path[edit.path.length - 1]}
            value={edit.value}
            path={edit.path}
            depth={depth}
            expand={expand}
            root={root}
            editing={editing}
            added
          />
        ) : null,
      )}
      {canAdd && editing && adding && (
        <ValueForm
          depth={depth}
          taken={names}
          onCancel={() => {
            onAdding(false)
            tree.focusRow(path)
          }}
          onCommit={(field, next) => {
            onAdding(false)
            editing.onEdit({ op: "set", path: [...path, field], value: next })
            tree.focusRow([...path, field])
          }}
        />
      )}
    </div>
  )
}

function TreeAction({
  label,
  onClick,
  touchless,
  tabIndex,
  children,
}: {
  label: string
  onClick: () => void
  /** `0` on the row the keyboard is on, `-1` on every other: a row's controls are that row's stops. */
  tabIndex: number
  /**
   * Left out where there is no pointer to hover with. There the controls
   * are a finger wide, and five of them leave a phone no room for the value;
   * the two that copy are the ones a document's own Copy covers.
   */
  touchless?: boolean
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      tabIndex={tabIndex}
      onClick={onClick}
      className={cn(
        "flex size-5 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground [@media(hover:none)]:size-8",
        touchless && "[@media(hover:none)]:hidden",
      )}
    >
      {children}
    </button>
  )
}

/**
 * One value as the shell writes it, in its kind's hue. The call around a
 * typed value — `ObjectId(`, `Long(` — is in the quiet voice: it says the
 * type, and the hue is spent on the value itself.
 */
export function BsonValue({
  node,
  className,
  onDoubleClick,
}: {
  node: BsonScalar
  className?: string
  onDoubleClick?: () => void
}) {
  const hue = valueClass(node.type)
  const call = /^([A-Za-z0-9]+\()([\s\S]*)(\))$/.exec(shellScalar(node))
  const clamp = (text: string) => (text.length > CLAMP ? `${text.slice(0, CLAMP)}…` : text)
  return (
    <span
      data-slot="bson-value"
      data-type={node.type}
      title={TYPE_LABEL[node.type]}
      className={className}
      onDoubleClick={onDoubleClick}
    >
      {node.type === "string" ? (
        <span className={hue}>{JSON.stringify(clamp(node.text))}</span>
      ) : call && node.type !== "javascript" ? (
        <>
          <span className="text-muted-foreground/70">{call[1]}</span>
          <span className={hue}>{clamp(call[2])}</span>
          <span className="text-muted-foreground/70">{call[3]}</span>
        </>
      ) : (
        <span className={hue}>{clamp(shellScalar(node))}</span>
      )}
    </span>
  )
}

const INPUT_LABEL: Record<InputType, string> = {
  ...Object.fromEntries(TYPED_INPUTS.map((type) => [type, TYPE_LABEL[type]])),
  ejson: "Extended JSON",
} as Record<InputType, string>

const INPUT_TYPES: InputType[] = [...TYPED_INPUTS, "ejson"]

const PLACEHOLDER: Partial<Record<InputType, string>> = {
  int: "42",
  long: "9007199254740993",
  double: "0.5",
  decimal: "19.99",
  date: "2026-05-01T12:00:00Z",
  objectId: "24 hexadecimal digits",
  ejson: '{ "nested": true }  or  [1, 2]',
}

/**
 * The form a value is given or changed with: its type, chosen from a list,
 * and its text. A new field asks for its name first. Enter stages it, Escape
 * lets it go; nothing is written until the document's update is sent.
 */
function ValueForm({
  depth,
  name,
  initial,
  taken,
  onCommit,
  onCancel,
}: {
  depth: number
  /** The field being edited. Absent: a new field, whose name is asked for. */
  name?: string
  initial?: BsonNode
  /** Names the document already has at this level, which a new field may not take. */
  taken?: ReadonlySet<string>
  onCommit: (field: string, value: BsonNode) => void
  onCancel: () => void
}) {
  const start = initial ? inputOf(initial) : { type: "string" as InputType, text: "" }
  const [field, setField] = useState("")
  const [type, setType] = useState<InputType>(start.type)
  const [text, setText] = useState(start.text)
  const [tried, setTried] = useState(false)
  const id = useId()

  const creating = name === undefined
  const read = typedValue(type, type === "null" ? "" : text)
  const fieldProblem = !creating
    ? null
    : !field
      ? "A field needs a name."
      : field.includes(".") || field.startsWith("$")
        ? "A name with a dot or a leading $ cannot be set in place: edit the whole document."
        : taken?.has(field)
          ? "The document already has a field of that name."
          : null
  const problem = fieldProblem ?? (read.ok ? null : read.message)

  const commit = () => {
    setTried(true)
    if (problem || !read.ok) return
    onCommit(creating ? field : name, read.node)
  }
  const keys = (event: React.KeyboardEvent) => {
    if (event.key === "Escape") {
      event.preventDefault()
      event.stopPropagation()
      onCancel()
    } else if (event.key === "Enter" && !event.shiftKey && type !== "ejson") {
      event.preventDefault()
      commit()
    }
  }

  return (
    <div
      data-slot="bson-value-form"
      className="my-0.5 rounded-md bg-surface-sunken py-1.5 pr-1.5"
      style={indent(depth)}
      onKeyDown={keys}
    >
      <div className="flex min-w-0 flex-wrap items-start gap-1.5 pl-5">
        {creating ? (
          <Input
            autoFocus
            value={field}
            spellCheck={false}
            autoComplete="off"
            placeholder="field"
            aria-label="Field name"
            className="h-9 w-36 px-2 font-mono sm:h-7 sm:text-xs"
            onChange={(event) => setField(event.target.value)}
          />
        ) : (
          <span className="flex h-7 shrink-0 items-center text-muted-foreground">{name}:</span>
        )}
        <Select
          value={type}
          onValueChange={(next) => {
            setType(next as InputType)
            // A boolean is one of two words: what was typed for another type is not one.
            if (next === "bool" && text !== "true" && text !== "false") setText("false")
          }}
        >
          <SelectTrigger
            size="sm"
            aria-label={creating ? "Type of the new field" : `Type of ${name}`}
            className="h-7 gap-1.5 px-2 font-sans text-xs data-[size=sm]:h-7 sm:data-[size=sm]:h-7"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent position="popper" align="start">
            {INPUT_TYPES.map((entry) => (
              <SelectItem key={entry} value={entry} className="text-xs">
                {INPUT_LABEL[entry]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {type === "null" ? (
          <span className="flex h-7 items-center text-muted-foreground/70">null</span>
        ) : type === "bool" ? (
          <Select value={text === "true" ? "true" : "false"} onValueChange={setText}>
            <SelectTrigger
              size="sm"
              aria-label={creating ? "Value of the new field" : `Value of ${name}`}
              className="h-7 gap-1.5 px-2 text-xs data-[size=sm]:h-7 sm:data-[size=sm]:h-7"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent position="popper" align="start">
              <SelectItem value="true" className="font-mono text-xs">
                true
              </SelectItem>
              <SelectItem value="false" className="font-mono text-xs">
                false
              </SelectItem>
            </SelectContent>
          </Select>
        ) : type === "ejson" ? (
          <CodeField
            id={id}
            autoFocus={!creating}
            value={text}
            invalid={tried && !read.ok}
            aria-label={creating ? "Value of the new field" : `Value of ${name}`}
            placeholder={PLACEHOLDER.ejson}
            className="min-h-16 min-w-48 flex-1 basis-64 bg-card dark:bg-card"
            onChange={setText}
            onSubmit={commit}
          />
        ) : (
          <Input
            id={id}
            autoFocus={!creating}
            value={text}
            spellCheck={false}
            autoComplete="off"
            aria-invalid={(tried && !read.ok) || undefined}
            aria-label={creating ? "Value of the new field" : `Value of ${name}`}
            placeholder={PLACEHOLDER[type]}
            className="h-9 min-w-32 flex-1 basis-48 px-2 font-mono sm:h-7 sm:text-xs"
            onChange={(event) => setText(event.target.value)}
            onFocus={(event) => !creating && event.target.select()}
          />
        )}
        <Button size="xs" variant="outline" className="h-7" onClick={commit}>
          <Check />
          {creating ? "Add" : "Set"}
        </Button>
        <Button
          size="icon-xs"
          variant="ghost"
          aria-label="Cancel"
          className="size-7"
          onClick={onCancel}
        >
          <Cross />
        </Button>
      </div>
      {tried && problem && (
        <p role="alert" className="pt-1 pl-5 font-sans text-hint text-destructive">
          {problem}
        </p>
      )}
    </div>
  )
}
