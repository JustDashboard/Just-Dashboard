"use client"

import { useId, useRef, useState } from "react"
import { MoreHorizontal } from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes } from "@/lib/format"
import { Segments } from "@/components/deploy/settings/segments"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import {
  DEFAULT_VALUE,
  KIND_HUE,
  formatCell,
  isDefault,
  parseInput,
  type CellValue,
  type EditValue,
  type GridColumn,
} from "@/components/database/grid"
// The text an editor opens on, and JSON laid out without being re-read, are
// the grid's own rules for typing a value; a second copy here would be a
// second parser of the same types.
import { indentJSON } from "@/components/database/grid/json-text"
import { editText, temporalHint } from "@/components/database/grid/values"
import { JsonDocTree } from "@/components/database/data/json-field"
import type { InspectedCell } from "@/components/database/data/rows"

/** Bytes of a file the inspector will stage as a value: it travels as hex inside one request. */
export const MAX_STAGED_BYTES = 1 << 20

/** A value the reader can type in one line. Longer, or with a line break, gets the tall field. */
const ONE_LINE = 80

/** The enum picker's entry for NULL: a value no label can be. */
const NULL_CHOICE = "\u0000null"

/**
 * One column of the inspected row: its name and what it is, the value in an
 * editor that suits its kind, and the three things a value can be besides a
 * value — NULL, the empty string, the column's default — each asked for by
 * name, because a blank field cannot say which of them it means.
 *
 * It edits nothing itself. What the reader settles on is handed to `onStage`
 * as the value the change set takes, and the field is redrawn from the set:
 * an edit made here and one made in the grid are the same edit.
 */
export function RowField({
  cell,
  keyWord,
  inserted,
  lock,
  allowDefault,
  whole,
  onStage,
  onRevert,
  children,
}: {
  cell: InspectedCell
  /** What the column is to the table's key — "key", "sort key" — or null when it is no part of it. */
  keyWord: string | null
  /** The row is a staged new one: an unset column takes its default. */
  inserted: boolean
  /** Why this field cannot be edited, or null when it can. */
  lock: string | null
  /** "Use the default" can be staged for this column. */
  allowDefault: boolean
  /** The whole of a value the page only carried the start of, once it was read. */
  whole?: CellValue
  onStage: (value: EditValue) => void
  onRevert: () => void
  /** What follows the control: a foreign key's peek, a preview's "load the rest". */
  children?: React.ReactNode
}) {
  const id = useId()
  const { column } = cell
  const value = whole !== undefined && !cell.edited ? whole : cell.value
  const unset = value === undefined || isDefault(value)
  const text = column.kind === "text" || column.kind === "unknown"
  const editable = lock === null

  const states: { key: string; label: string; detail?: string; run: () => void }[] = []
  if (editable && column.nullable !== false && value !== null) {
    states.push({ key: "null", label: "NULL", run: () => onStage(null) })
  }
  if (editable && text && value !== "") {
    states.push({ key: "empty", label: "Empty string", run: () => onStage("") })
  }
  if (editable && allowDefault && !unset) {
    states.push({
      key: "default",
      label: "Default",
      detail: column.defaultExpr,
      run: () => onStage(DEFAULT_VALUE),
    })
  }
  if (editable && column.kind === "uuid") {
    states.push({ key: "uuid", label: "A new UUID", run: () => onStage(crypto.randomUUID()) })
  }
  const hint = lock
    ? lock
    : cell.edited && cell.original !== undefined
      ? `Was ${formatCell(cell.original, column).text}`
      : unset && column.defaultExpr
        ? `Default: ${column.defaultExpr}`
        : column.comment

  return (
    <div
      data-slot="row-field"
      data-edited={cell.edited || undefined}
      className={cn(
        "min-w-0 space-y-1 border-l-2 py-2 pr-3 pl-2.5",
        cell.edited ? "border-l-(--git-modified)" : "border-l-transparent",
      )}
    >
      <div className="flex min-w-0 items-center gap-2">
        <label htmlFor={id} className="min-w-0 truncate font-mono text-xs font-medium">
          {column.name}
        </label>
        {keyWord && <Tag className="shrink-0">{keyWord}</Tag>}
        {column.generated && <Tag>computed</Tag>}
        <Tag mono className="ml-auto max-w-[50%] min-w-0 truncate" title={column.typeName}>
          {column.typeName || "?"}
        </Tag>
      </div>

      <div className="flex min-w-0 items-start gap-1">
        <div className="min-w-0 flex-1 space-y-1">
          {editable ? (
            <Control id={id} column={column} value={value} onStage={onStage} />
          ) : (
            <Reading id={id} column={column} value={value} />
          )}
        </div>
        {states.length > 0 && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                size="icon-xs"
                variant="ghost"
                aria-label={`Set ${column.name} to…`}
                className="mt-0.5 shrink-0 text-muted-foreground"
              >
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="min-w-44">
              <DropdownMenuLabel>Set to</DropdownMenuLabel>
              {states.map((state) => (
                <DropdownMenuItem key={state.key} onSelect={state.run}>
                  <span className="shrink-0">{state.label}</span>
                  {state.detail && (
                    <span className="ml-auto max-w-40 min-w-0 truncate pl-3 font-mono text-hint text-muted-foreground">
                      {state.detail}
                    </span>
                  )}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>

      {(hint || (cell.edited && !inserted)) && (
        <div className="flex min-w-0 items-center gap-1">
          <p
            className={cn("min-w-0 flex-1 text-hint text-muted-foreground", !lock && "truncate")}
            title={lock ? undefined : hint}
          >
            {hint}
          </p>
          {cell.edited && !inserted && (
            <Button
              type="button"
              size="xs"
              variant="ghost"
              className="h-5 shrink-0 px-1.5 text-hint text-muted-foreground"
              onClick={onRevert}
            >
              Revert
            </Button>
          )}
        </div>
      )}
      {children}
    </div>
  )
}

const ZONED = /tz|with time zone|offset/i

/**
 * The text a field opens on: the grid's, less a zone the column does not keep.
 *
 * A `timestamp without time zone` travels as an instant at UTC, and the grid's
 * editor spells that offset out. In a column that stores none it reads as a
 * property of the value — `03:04:05+00:00` — that the reader would then be
 * careful to keep, or to change. The field shows what the column holds.
 */
function openText(value: EditValue | undefined, column: GridColumn): string {
  const text = editText(value, column)
  if ((column.kind === "datetime" || column.kind === "time") && !ZONED.test(column.typeName)) {
    return text.replace(/(?:Z|[+-]00:00)$/, "")
  }
  return text
}

/** What an empty control says it holds: nothing typed is one of three different things. */
function vacancy(value: EditValue | undefined): string {
  if (value === undefined || isDefault(value)) return "DEFAULT"
  if (value === null) return "NULL"
  if (value === "") return '""  (empty string)'
  return ""
}

/* ---------------------------------------------------------------- reading */

/** A value that cannot be edited here, drawn whole and typed. */
function Reading({
  id,
  column,
  value,
}: {
  id: string
  column: GridColumn
  value: EditValue | undefined
}) {
  const shown = formatCell(value, column)
  if (shown.tone !== "value") {
    return (
      <p id={id} className="font-mono text-xs text-muted-foreground/70 italic">
        {shown.text}
      </p>
    )
  }
  if (column.kind === "json" && !isDefault(value) && value !== undefined) {
    return (
      <div id={id} className="max-h-64 overflow-auto">
        <JsonDocTree
          text={typeof value === "string" ? value : openText(value, column)}
          label={column.name}
        />
      </div>
    )
  }
  if (column.kind === "binary") {
    return (
      <p id={id} className={cn("font-mono text-xs break-all", KIND_HUE.binary)}>
        {shown.lead && <span className="text-muted-foreground">{shown.lead} </span>}
        {shown.text}
      </p>
    )
  }
  const whole = openText(value, column)
  return (
    <p
      id={id}
      className={cn(
        "max-h-48 overflow-y-auto font-mono text-xs break-words whitespace-pre-wrap",
        KIND_HUE[column.kind],
      )}
    >
      {column.kind === "datetime" || column.kind === "date" || column.kind === "time"
        ? shown.text
        : whole}
    </p>
  )
}

/* ---------------------------------------------------------------- editing */

function Control({
  id,
  column,
  value,
  onStage,
}: {
  id: string
  column: GridColumn
  value: EditValue | undefined
  onStage: (value: EditValue) => void
}) {
  if (column.kind === "boolean")
    return <BooleanControl column={column} value={value} onStage={onStage} />
  if (column.kind === "enum" && column.enumValues && column.enumValues.length > 0) {
    return <EnumControl id={id} column={column} value={value} onStage={onStage} />
  }
  if (column.kind === "json")
    return <JsonControl id={id} column={column} value={value} onStage={onStage} />
  if (column.kind === "binary")
    return <BinaryControl id={id} column={column} value={value} onStage={onStage} />
  return <TextControl id={id} column={column} value={value} onStage={onStage} />
}

type ControlProps = {
  id: string
  column: GridColumn
  value: EditValue | undefined
  onStage: (value: EditValue) => void
}

/**
 * The draft of a typed value, and what happens when the reader is done with
 * it. A field left as it was opened stages nothing — that is how a NULL
 * stays NULL when the reader only tabbed through — and a draft the column
 * cannot hold stays in the field with the reason under it.
 */
function useDraft({ column, value, onStage }: Omit<ControlProps, "id">) {
  const opened = openText(value, column)
  const [draft, setDraft] = useState(opened)
  const [error, setError] = useState<string>()
  // The set changed under the field — an edit in the grid, an undo, a revert:
  // the field shows what is staged now, not what was being typed before.
  const [seen, setSeen] = useState(opened)
  if (seen !== opened) {
    setSeen(opened)
    setDraft(opened)
    setError(undefined)
  }
  const commit = (text = draft) => {
    if (text === opened) {
      setError(undefined)
      return
    }
    const parsed = parseInput(text, column)
    if (!parsed.ok) {
      setError(parsed.error)
      return
    }
    setError(undefined)
    onStage(parsed.value)
  }
  return { opened, draft, setDraft, error, setError, commit }
}

function placeholder(column: GridColumn, value: EditValue | undefined): string {
  const empty = vacancy(value)
  if (empty) return empty
  if (column.kind === "date" || column.kind === "time" || column.kind === "datetime") {
    return temporalHint(column.kind, column.typeName)
  }
  return ""
}

function TextControl({ id, column, value, onStage }: ControlProps) {
  const { opened, draft, setDraft, error, commit } = useDraft({ column, value, onStage })
  const tall = opened.length > ONE_LINE || opened.includes("\n")
  const numeric = column.kind === "number"
  const shared = {
    id,
    value: draft,
    spellCheck: false,
    autoComplete: "off",
    "aria-invalid": error ? true : undefined,
    placeholder: placeholder(column, value),
    onBlur: () => commit(),
  }
  return (
    <>
      {tall ? (
        <Textarea
          {...shared}
          rows={3}
          className={cn(
            "max-h-64 min-h-16 px-2 py-1.5 font-mono placeholder:italic sm:text-xs",
            KIND_HUE[column.kind],
          )}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
              event.preventDefault()
              commit()
            }
          }}
        />
      ) : (
        <div className="flex min-w-0 items-center gap-1">
          <Input
            {...shared}
            inputMode={numeric ? "decimal" : undefined}
            className={cn(
              "px-2 font-mono placeholder:italic sm:h-7 sm:text-xs",
              KIND_HUE[column.kind],
            )}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault()
                commit()
              }
            }}
          />
        </div>
      )}
      {error && (
        <p role="alert" className="text-hint text-destructive">
          {error}
        </p>
      )}
    </>
  )
}

function BooleanControl({ column, value, onStage }: Omit<ControlProps, "id">) {
  const current =
    value === true || value === "true" || value === "t" || value === "1" || value === 1
      ? "true"
      : value === false || value === "false" || value === "f" || value === "0" || value === 0
        ? "false"
        : ""
  return (
    <div className="flex min-w-0 items-center gap-2">
      <Segments
        label={column.name}
        value={current}
        options={[
          { value: "true", label: "true", mono: true },
          { value: "false", label: "false", mono: true },
        ]}
        className="[&_[data-slot=toggle-group-item]]:h-7"
        onChange={(next) => onStage(next === "true")}
      />
      {current === "" && (
        <span className="font-mono text-xs text-muted-foreground/70 italic">{vacancy(value)}</span>
      )}
    </div>
  )
}

function EnumControl({ id, column, value, onStage }: ControlProps) {
  const labels = column.enumValues ?? []
  const current = typeof value === "string" && labels.includes(value) ? value : undefined
  return (
    <Select
      value={current ?? ""}
      onValueChange={(next) => {
        onStage(next === NULL_CHOICE ? null : next)
      }}
    >
      <SelectTrigger
        id={id}
        size="sm"
        className={cn("w-full font-mono text-xs sm:h-7", KIND_HUE.enum)}
      >
        <SelectValue
          placeholder={
            <span className="text-muted-foreground/70 italic">
              {vacancy(value) || (typeof value === "string" ? value : "")}
            </span>
          }
        />
      </SelectTrigger>
      <SelectContent>
        {labels.map((label) => (
          <SelectItem key={label} value={label} className="font-mono text-xs">
            {label}
          </SelectItem>
        ))}
        {column.nullable !== false && (
          <>
            <SelectSeparator />
            <SelectItem value={NULL_CHOICE} className="font-mono text-xs italic">
              NULL
            </SelectItem>
          </>
        )}
      </SelectContent>
    </Select>
  )
}

function JsonControl({ id, column, value, onStage }: ControlProps) {
  const { opened, draft, setDraft, error, setError, commit } = useDraft({ column, value, onStage })
  const [mode, setMode] = useState<"tree" | "text">("tree")
  const empty = opened === ""
  const shown = empty ? "text" : mode
  return (
    <div className="min-w-0 space-y-1">
      <div className="flex items-center gap-1">
        {!empty && (
          <Segments
            label={`How ${column.name} is shown`}
            value={mode}
            options={[
              { value: "tree", label: "Tree" },
              { value: "text", label: "Text" },
            ]}
            className="[&_[data-slot=toggle-group-item]]:h-6"
            onChange={setMode}
          />
        )}
        {shown === "text" && (
          <Button
            type="button"
            size="xs"
            variant="ghost"
            className="ml-auto text-muted-foreground"
            onClick={() => {
              const laid = indentJSON(draft)
              if (laid === null) setError("Not valid JSON")
              else {
                setError(undefined)
                setDraft(laid)
              }
            }}
          >
            Format
          </Button>
        )}
      </div>
      {shown === "tree" ? (
        // Each change made in the tree is the document with one node
        // rewritten, staged like any other value of the field. The tree reads
        // the text as it was stored where it has it, not the laid-out copy the
        // text view opens on: an engine that keeps a document's own spacing
        // gets back the spacing it had.
        <div className="max-h-72 overflow-auto">
          <JsonDocTree
            text={typeof value === "string" ? value : opened}
            label={column.name}
            onChange={(next) => commit(next)}
          />
        </div>
      ) : (
        <Textarea
          id={id}
          value={draft}
          rows={4}
          spellCheck={false}
          aria-invalid={error ? true : undefined}
          placeholder={vacancy(value)}
          className={cn(
            "max-h-72 min-h-20 px-2 py-1.5 font-mono placeholder:italic sm:text-xs",
            KIND_HUE.json,
          )}
          onChange={(event) => setDraft(event.target.value)}
          onBlur={() => commit()}
          onKeyDown={(event) => {
            if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
              event.preventDefault()
              commit()
            }
          }}
        />
      )}
      {error && (
        <p role="alert" className="text-hint text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}

/** The bytes of a file as the `\x…` text a binary column takes. */
async function fileHex(file: File): Promise<string> {
  const data = new Uint8Array(await file.arrayBuffer())
  let hex = ""
  for (const byte of data) hex += byte.toString(16).padStart(2, "0")
  return `\\x${hex}`
}

/**
 * Bytes are not typed. A short value can be — as hex — and anything else is
 * replaced from a file, which is how bytes get anywhere.
 */
function BinaryControl({ id, column, value, onStage }: ControlProps) {
  const { draft, setDraft, error, setError, commit } = useDraft({ column, value, onStage })
  const picker = useRef<HTMLInputElement>(null)
  const shown = formatCell(value, column)
  const short = typeof value !== "string" || value.length <= 2 + 64 * 2
  return (
    <div className="min-w-0 space-y-1">
      {short ? (
        <Input
          id={id}
          value={draft}
          spellCheck={false}
          autoComplete="off"
          aria-invalid={error ? true : undefined}
          placeholder={vacancy(value) || "\\x followed by hex digits"}
          className={cn("px-2 font-mono placeholder:italic sm:h-7 sm:text-xs", KIND_HUE.binary)}
          onChange={(event) => setDraft(event.target.value)}
          onBlur={() => commit()}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault()
              commit()
            }
          }}
        />
      ) : (
        <p id={id} className={cn("font-mono text-xs break-all", KIND_HUE.binary)}>
          {shown.lead && <span className="text-muted-foreground">{shown.lead} </span>}
          {shown.text}
        </p>
      )}
      <input
        ref={picker}
        type="file"
        hidden
        onChange={(event) => {
          const file = event.currentTarget.files?.[0]
          event.currentTarget.value = ""
          if (!file) return
          if (file.size > MAX_STAGED_BYTES) {
            setError(
              `${file.name} is ${bytes(file.size)}; a value staged here is at most ${bytes(MAX_STAGED_BYTES)}`,
            )
            return
          }
          setError(undefined)
          void fileHex(file).then(onStage)
        }}
      />
      <Button type="button" size="xs" variant="outline" onClick={() => picker.current?.click()}>
        Replace from a file
      </Button>
      {error && (
        <p role="alert" className="text-hint text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}
