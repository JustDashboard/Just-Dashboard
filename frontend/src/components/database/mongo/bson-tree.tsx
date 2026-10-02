"use client"

import { useId, useState } from "react"
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
import { rowReveal } from "@/components/icon-action"
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
import { addedUnder, editAt, type Edit, type Edits } from "@/components/database/mongo/changes"
import { CodeField } from "@/components/database/mongo/code-field"
import { valueClass } from "@/components/database/mongo/kinds"

/** How many entries of one object or array are drawn before the rest are asked for. */
const PAGE = 100

/** The most characters of one string drawn in a row. */
const CLAMP = 240

/** What an editable tree is given: the staged edits and the two ways they change. */
export type TreeEditing = {
  edits: Edits
  onEdit: (edit: Edit) => void
  onRevert: (path: Segment[]) => void
}

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
 */
export function BsonTree({
  root,
  expand = 1,
  editing,
  adding = false,
  onAdding,
  className,
}: {
  root: BsonNode
  /** How many levels are open to begin with. */
  expand?: number
  editing?: TreeEditing
  /** The form for a new top-level field is open; the owner's own control opens it. */
  adding?: boolean
  onAdding?: (adding: boolean) => void
  className?: string
}) {
  return (
    <div
      data-slot="bson-tree"
      className={cn("min-w-0 font-mono text-xs leading-relaxed", className)}
    >
      {root.type === "object" || root.type === "array" ? (
        <Children
          node={root}
          path={[]}
          depth={0}
          expand={expand}
          editing={editing}
          root={root}
          adding={adding}
          onAdding={onAdding ?? noop}
        />
      ) : (
        <Row value={root} path={[]} depth={0} expand={expand} root={root} />
      )}
    </div>
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
}

const indent = (depth: number) => ({ paddingLeft: `${depth * 14 + 2}px` })

/** A value's staged state, as the hue of its gutter mark. */
const STATE_MARK = {
  changed: "bg-(--git-modified)",
  added: "bg-(--git-added)",
  removed: "bg-(--git-deleted)",
} as const

function Row({ name, value, path, depth, expand, root, editing, added }: RowProps) {
  const [open, setOpen] = useState(depth < expand)
  const [form, setForm] = useState(false)
  const [adding, setAdding] = useState(false)

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

  if (form && editing) {
    return (
      <ValueForm
        depth={depth}
        name={label}
        initial={shown}
        onCancel={() => setForm(false)}
        onCommit={(_field, next) => {
          setForm(false)
          // Back to what the document holds is no edit at all.
          if (!added && sameValue(next, value)) editing.onRevert(path)
          else editing.onEdit({ op: "set", path, value: next })
        }}
      />
    )
  }

  return (
    <div>
      <div
        data-state={state}
        className={cn(
          "group/row relative flex min-h-6 min-w-0 items-center gap-1 rounded-sm pr-1 hover:bg-row-hover",
          state === "removed" && "text-muted-foreground line-through decoration-(--git-deleted)",
        )}
        style={indent(depth)}
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
            // A disclosure, not a tree widget: nothing here is selected, and
            // every row's controls are reached with Tab as they are drawn.
            aria-expanded={open}
            aria-label={`${open ? "Collapse" : "Expand"} ${label}`}
            onClick={() => setOpen(!open)}
            className="flex size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground"
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
        {state && (
          <span className="sr-only">
            {state === "changed" ? ", changed" : state === "added" ? ", added" : ", to be removed"}
          </span>
        )}
        <div
          className={cn(
            "ml-auto flex shrink-0 items-center pl-2 focus-within:opacity-100",
            rowReveal("row"),
          )}
        >
          {state && editing && (
            <TreeAction
              label={`Undo the change to ${label}`}
              onClick={() => editing.onRevert(path)}
            >
              <RotateCounterClockwise className="size-3" />
            </TreeAction>
          )}
          {canEdit && (
            <TreeAction label={`Edit ${label}`} onClick={() => setForm(true)}>
              <Pencil className="size-3" />
            </TreeAction>
          )}
          {canAddInside && (
            <TreeAction
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
              label={`Remove ${label}`}
              onClick={() => editing.onEdit({ op: "unset", path })}
            >
              <Trash className="size-3" />
            </TreeAction>
          )}
          <TreeAction
            touchless
            label={`Copy the path to ${label}`}
            onClick={() => void copyText(dotted(path) ?? JSON.stringify(path), "Path copied")}
          >
            <Route className="size-3" />
          </TreeAction>
          <TreeAction
            touchless
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
  editing,
  adding,
  onAdding,
}: {
  node: Extract<BsonNode, { type: "object" | "array" }>
  path: Segment[]
  depth: number
  expand: number
  root: BsonNode
  editing?: TreeEditing
  /** The form for a new field is open at the end of this level. */
  adding: boolean
  onAdding: (adding: boolean) => void
}) {
  const [shown, setShown] = useState(PAGE)
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
    <div>
      {entries.slice(0, shown).map(([key, child]) => (
        <Row
          key={key}
          name={key}
          value={child}
          path={[...path, key]}
          depth={depth}
          expand={expand}
          root={root}
          editing={editing}
        />
      ))}
      {entries.length > shown && (
        <button
          type="button"
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
          onCancel={() => onAdding(false)}
          onCommit={(field, next) => {
            onAdding(false)
            editing.onEdit({ op: "set", path: [...path, field], value: next })
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
  children,
}: {
  label: string
  onClick: () => void
  /**
   * Left out where there is no pointer to hover with. There a row's controls
   * are always drawn, and four of them on every field leave a phone no room
   * for the value; the two that copy are the ones a document's own Copy covers.
   */
  touchless?: boolean
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className={cn(
        "flex size-5 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground",
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
