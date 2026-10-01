"use client"

import { createContext, useContext, useEffect, useId, useMemo, useRef, useState } from "react"
import { Check } from "@/components/icons"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover"
import { cn } from "@/lib/utils"
import { indentJSON } from "./json-text"
import { numberSpec } from "./kinds"
import {
  DEFAULT_VALUE,
  editText,
  holdsText,
  isDefault,
  parseInput,
  randomUUID,
  temporalHint,
  type EditValue,
} from "./values"
import type { GridColumn } from "./types"

/**
 * The editors a cell opens.
 *
 * Three shapes, chosen by what the column holds. A value that fits on a line is
 * typed in place, in the cell, with a slim strip under it that says what the
 * column takes and offers NULL and the default. A closed set — a boolean, an
 * enum — is a list, because the valid answers are known and typing one is how a
 * typo gets in. And anything with structure or more than a line — JSON, a
 * paragraph — opens a larger surface with room to read what is being changed.
 *
 * None of them writes anything. Each hands a value back to the grid, which
 * stages it; an editor that is closed without a change hands back nothing.
 */

/** Where the active cell goes once an edit is finished. `none` leaves focus alone. */
export type EditMove = "down" | "up" | "right" | "left" | "stay" | "none"

export interface GridEditorApi {
  /** The character that started the edit, when typing did. It replaces the value. */
  seed: string | null
  /** Enter and a double-click select the value; F2 puts the caret at its end. */
  caret: "select" | "end"
  /** Open the larger surface straight away. */
  expanded: boolean
  /** True when more than one cell is selected, so one value can fill them all. */
  canFill: boolean
  /** Whether this cell may be sent back to its column default (not every engine can, for a row that exists). */
  canDefault: boolean
  /**
   * Closes the editor. `result` is the value to stage, or null when nothing
   * changed. `fill` carries the typed text for every other cell of the range.
   * False when the grid would not stage it; the editor then stays open.
   */
  finish: (result: { value: EditValue } | null, move: EditMove, fill?: string) => boolean
  /**
   * Lets the grid ask the open editor to settle before it moves somewhere
   * else, and to take the keyboard back — the menu that opened it hands focus
   * to wherever it was before, which is the cell and not the field in it.
   */
  register: (handle: GridEditorHandle | null) => void
}

export interface GridEditorHandle {
  /** False when the editor holds something it cannot stage. */
  settle: () => boolean
  focus: () => void
}

export const GridEditorContext = createContext<GridEditorApi | null>(null)

/**
 * What every editor surface carries.
 *
 * The attribute marks it as part of an editor, so the grid leaves its keys and
 * clicks alone. The two handlers keep its pointer to itself: an editor is a
 * child of its cell, in the document or through a portal, and the cells sit
 * inside the element that opens the grid's context menu — which would answer a
 * right-click or a long press in the field by opening that menu over it,
 * taking the browser's own (the one with Paste in it) and the focus with it.
 */
export const EDITOR_PROPS = {
  "data-grid-editor": "",
  onContextMenu: (event: React.MouseEvent) => event.stopPropagation(),
  onPointerDown: (event: React.PointerEvent) => event.stopPropagation(),
} as const

function columnHint(column: GridColumn): string {
  switch (column.kind) {
    case "number": {
      const spec = numberSpec(column.typeName)
      if (spec.class === "integer") return `${column.typeName} · ${spec.min} to ${spec.max}`
      if (spec.class === "money") return `${column.typeName} · digits, or as the engine prints it`
      return column.typeName
    }
    case "date":
    case "time":
    case "datetime":
      return temporalHint(column.kind, column.typeName)
    case "uuid":
      return "8-4-4-4-12 hex digits"
    case "binary":
      return "\\x followed by hex digits"
    case "json":
      return "JSON"
    case "array":
      return `${column.typeName} · written as the engine writes it, {a,b}`
    default:
      return column.typeName
  }
}

export function CellEditor({
  column,
  value,
}: {
  column: GridColumn
  value: EditValue | undefined
}) {
  const editor = useContext(GridEditorContext)
  if (!editor) return null
  if (column.kind === "boolean") {
    return <ListEditor column={column} value={value} editor={editor} labels={["true", "false"]} />
  }
  if (column.kind === "enum" && column.enumValues && column.enumValues.length > 0) {
    return <ListEditor column={column} value={value} editor={editor} labels={column.enumValues} />
  }
  return <TextEditor column={column} value={value} editor={editor} />
}

/* ------------------------------------------------------------------- text */

/** Past this, a value is read in the larger surface rather than scrolled along one line. */
const LONG_TEXT = 160

function TextEditor({
  column,
  value,
  editor,
}: {
  column: GridColumn
  value: EditValue | undefined
  editor: GridEditorApi
}) {
  const [initial] = useState(() => editText(value, column))
  const [text, setText] = useState(() => editor.seed ?? initial)
  const [expanded, setExpanded] = useState(
    () =>
      editor.expanded ||
      column.kind === "json" ||
      initial.includes("\n") ||
      initial.length > LONG_TEXT,
  )

  const parsed = useMemo(() => parseInput(text, column), [text, column])
  // Opening a cell and leaving it is not an edit: a NULL that was only looked
  // at must not come back as an empty string.
  const dirty = editor.seed !== null || text !== initial
  const starting = value === null || value === undefined || isDefault(value)

  const settle = (move: EditMove, fill?: boolean): boolean => {
    if (!dirty) return editor.finish(null, move)
    if (!parsed.ok) return false
    return editor.finish({ value: parsed.value }, move, fill ? text : undefined)
  }
  const settleRef = useRef(settle)
  useEffect(() => {
    settleRef.current = settle
  })
  // The field, in whichever of the two shapes is open.
  const inputRef = useRef<HTMLInputElement>(null)
  const areaRef = useRef<HTMLTextAreaElement>(null)
  const { register } = editor
  useEffect(() => {
    register({
      settle: () => settleRef.current("none"),
      focus: () => (areaRef.current ?? inputRef.current)?.focus({ preventScroll: true }),
    })
    return () => register(null)
  }, [register])

  const shared = {
    column,
    text,
    setText,
    error: dirty && !parsed.ok ? parsed.error : null,
    settle,
    setNull: column.nullable ? () => editor.finish({ value: null }, "stay") : null,
    // An empty field on a cell that holds nothing is read as "left alone", so
    // the empty string has to be asked for by name, as NULL is.
    setEmpty: holdsText(column) && starting ? () => editor.finish({ value: "" }, "stay") : null,
    setDefault: editor.canDefault ? () => editor.finish({ value: DEFAULT_VALUE }, "stay") : null,
    cancel: () => void editor.finish(null, "stay"),
  }

  if (expanded) return <AreaEditor {...shared} fieldRef={areaRef} />
  return (
    <InlineEditor
      {...shared}
      fieldRef={inputRef}
      caret={editor.seed !== null ? "end" : editor.caret}
      canFill={editor.canFill}
      placeholder={starting ? (value === null ? "NULL" : "default") : ""}
      onExpand={
        column.kind === "text" || column.kind === "unknown" ? () => setExpanded(true) : null
      }
    />
  )
}

interface TextEditorShared {
  column: GridColumn
  text: string
  setText: (text: string) => void
  error: string | null
  settle: (move: EditMove, fill?: boolean) => boolean
  /** These three stage a value by name and close the editor; false when the grid refused. */
  setNull: (() => boolean) | null
  setEmpty: (() => boolean) | null
  setDefault: (() => boolean) | null
  cancel: () => void
}

/** Keeps a press on a strip button from taking focus out of the field it belongs to. */
const keepFocus = (event: React.PointerEvent) => event.preventDefault()

function ValueButtons({
  setNull,
  setEmpty,
  setDefault,
}: Pick<TextEditorShared, "setNull" | "setEmpty" | "setDefault">) {
  return (
    <>
      {setEmpty && (
        <Button
          type="button"
          size="xs"
          variant="outline"
          title="Set the empty string"
          onPointerDown={keepFocus}
          onClick={setEmpty}
        >
          Empty
        </Button>
      )}
      {setNull && (
        <Button
          type="button"
          size="xs"
          variant="outline"
          title="Set NULL (Ctrl+Alt+N)"
          onPointerDown={keepFocus}
          onClick={setNull}
        >
          NULL
        </Button>
      )}
      {setDefault && (
        <Button
          type="button"
          size="xs"
          variant="outline"
          title="Use the column default (Ctrl+Alt+D)"
          onPointerDown={keepFocus}
          onClick={setDefault}
        >
          Default
        </Button>
      )}
    </>
  )
}

/**
 * Ctrl+Alt+N and Ctrl+Alt+D, wherever an editor has the keyboard. Null when the
 * key is neither; otherwise whether the value was staged and the editor closed.
 */
function valueShortcut(
  event: React.KeyboardEvent,
  { setNull, setDefault }: Pick<TextEditorShared, "setNull" | "setDefault">,
): boolean | null {
  if (!(event.ctrlKey || event.metaKey) || !event.altKey) return null
  const key = event.key.toLowerCase()
  const stage = key === "n" ? setNull : key === "d" ? setDefault : null
  if (!stage) return null
  event.preventDefault()
  return stage()
}

function InlineEditor({
  column,
  text,
  setText,
  error,
  settle,
  setNull,
  setEmpty,
  setDefault,
  cancel,
  fieldRef,
  caret,
  canFill,
  placeholder,
  onExpand,
}: TextEditorShared & {
  fieldRef: React.RefObject<HTMLInputElement | null>
  caret: "select" | "end"
  canFill: boolean
  placeholder: string
  onExpand: (() => void) | null
}) {
  const hintId = useId()
  // The editor is unmounted by the same call that settles it, and the blur that
  // follows must not settle it a second time.
  const closed = useRef(false)

  useEffect(() => {
    const input = fieldRef.current
    if (!input) return
    input.focus({ preventScroll: true })
    if (caret === "select") input.select()
    else input.setSelectionRange(input.value.length, input.value.length)
  }, [caret, fieldRef])

  const finish = (move: EditMove, fill?: boolean) => {
    if (settle(move, fill)) closed.current = true
  }

  return (
    <Popover open>
      <PopoverAnchor asChild>
        <input
          {...EDITOR_PROPS}
          ref={fieldRef}
          value={text}
          placeholder={placeholder}
          spellCheck={false}
          autoComplete="off"
          aria-label={`Edit ${column.name}`}
          aria-invalid={error ? true : undefined}
          aria-describedby={hintId}
          className={cn(
            "absolute inset-0 z-10 w-full min-w-0 bg-popover px-2 font-mono text-xs text-foreground focus-ring-inset placeholder:text-muted-foreground/60 max-sm:text-base",
            column.kind === "number" && "text-right",
          )}
          onChange={(event) => setText(event.target.value)}
          onBlur={(event) => {
            if (closed.current) return
            const next = event.relatedTarget as HTMLElement | null
            if (next?.closest("[data-grid-editor]")) return
            finish("none")
          }}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing) return
            const staged = valueShortcut(event, { setNull, setDefault })
            if (staged !== null) {
              if (staged) closed.current = true
              return
            }
            if (event.key === "Escape") {
              event.preventDefault()
              closed.current = true
              cancel()
            } else if (event.key === "Enter") {
              event.preventDefault()
              if (event.shiftKey && onExpand) onExpand()
              else if (event.ctrlKey || event.metaKey) finish("stay", canFill)
              else finish(event.shiftKey ? "up" : "down")
            } else if (event.key === "Tab") {
              event.preventDefault()
              finish(event.shiftKey ? "left" : "right")
            }
          }}
        />
      </PopoverAnchor>
      <PopoverContent
        {...EDITOR_PROPS}
        side="bottom"
        align="start"
        sideOffset={2}
        hideWhenDetached
        className="flex w-auto max-w-[min(30rem,calc(100vw-1rem))] items-center gap-1.5 px-2 py-1"
        onOpenAutoFocus={(event) => event.preventDefault()}
        onCloseAutoFocus={(event) => event.preventDefault()}
        onInteractOutside={(event) => event.preventDefault()}
      >
        <span
          id={hintId}
          aria-live="polite"
          className={cn(
            "min-w-0 truncate text-hint",
            error ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {error ?? columnHint(column)}
        </span>
        <ValueButtons setNull={setNull} setEmpty={setEmpty} setDefault={setDefault} />
        {column.kind === "uuid" && (
          <Button
            type="button"
            size="xs"
            variant="outline"
            onPointerDown={keepFocus}
            onClick={() => setText(randomUUID())}
          >
            Generate
          </Button>
        )}
        {onExpand && (
          <Button
            type="button"
            size="xs"
            variant="outline"
            title="Open a larger editor (Shift+Enter)"
            onPointerDown={keepFocus}
            onClick={onExpand}
          >
            Expand
          </Button>
        )}
      </PopoverContent>
    </Popover>
  )
}

function AreaEditor({
  column,
  text,
  setText,
  error,
  settle,
  setNull,
  setEmpty,
  setDefault,
  cancel,
  fieldRef,
}: TextEditorShared & { fieldRef: React.RefObject<HTMLTextAreaElement | null> }) {
  const hintId = useId()
  const json = column.kind === "json"

  // Whitespace only: the numbers and the order of the keys stay as typed. Text
  // that is not JSON is left alone — the line under the field already says why.
  const format = () => setText(indentJSON(text.trim()) ?? text)

  return (
    <Popover open>
      <PopoverAnchor asChild>
        <span aria-hidden className="pointer-events-none absolute inset-0" />
      </PopoverAnchor>
      <PopoverContent
        {...EDITOR_PROPS}
        side="bottom"
        align="start"
        sideOffset={2}
        hideWhenDetached
        collisionPadding={8}
        className="flex w-[min(36rem,calc(100vw-1rem))] flex-col gap-0 p-0"
        onOpenAutoFocus={(event) => {
          event.preventDefault()
          const area = fieldRef.current
          if (!area) return
          area.focus({ preventScroll: true })
          area.setSelectionRange(area.value.length, area.value.length)
        }}
        onCloseAutoFocus={(event) => event.preventDefault()}
        onEscapeKeyDown={(event) => {
          event.preventDefault()
          cancel()
        }}
        // A press outside settles the edit, as leaving a cell does. One that
        // cannot be settled keeps the editor open rather than losing the text.
        onInteractOutside={(event) => {
          event.preventDefault()
          settle("none")
        }}
        onKeyDown={(event) => {
          if (valueShortcut(event, { setNull, setDefault }) !== null) return
          if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
            event.preventDefault()
            settle("stay")
          }
        }}
      >
        <div className="flex min-h-9 items-center gap-2 border-b border-hairline px-3 py-1.5">
          <span className="min-w-0 truncate text-body font-medium">{column.name}</span>
          <Tag mono className="min-w-0 truncate">
            {column.typeName}
          </Tag>
          {json && (
            <Button type="button" size="xs" variant="ghost" className="ml-auto" onClick={format}>
              Format
            </Button>
          )}
        </div>
        <textarea
          ref={fieldRef}
          value={text}
          spellCheck={false}
          aria-label={`Edit ${column.name}`}
          aria-invalid={error ? true : undefined}
          aria-describedby={hintId}
          className="block h-56 min-h-24 w-full resize-y bg-surface-sunken p-3 font-mono text-xs leading-relaxed whitespace-pre focus-ring-inset max-sm:text-base"
          onChange={(event) => setText(event.target.value)}
        />
        <div className="flex flex-wrap items-center gap-1.5 border-t border-hairline px-3 py-2">
          <span
            id={hintId}
            aria-live="polite"
            className={cn(
              "mr-auto min-w-0 truncate text-hint",
              error ? "text-destructive" : "text-muted-foreground",
            )}
          >
            {error ?? (json ? "Checked when applied" : columnHint(column))}
          </span>
          <ValueButtons setNull={setNull} setEmpty={setEmpty} setDefault={setDefault} />
          <Button type="button" size="xs" variant="outline" onClick={cancel}>
            Cancel
          </Button>
          <Button
            type="button"
            size="xs"
            title="Apply (Ctrl+Enter)"
            disabled={Boolean(error)}
            onClick={() => settle("stay")}
          >
            Apply
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}

/* ------------------------------------------------------------------- list */

interface ListOption {
  key: string
  label: string
  value: EditValue
  /** NULL and Default are not values of the column: they are drawn apart. */
  special?: boolean
}

function ListEditor({
  column,
  value,
  editor,
  labels,
}: {
  column: GridColumn
  value: EditValue | undefined
  editor: GridEditorApi
  labels: readonly string[]
}) {
  const options = useMemo(() => {
    const list: ListOption[] = labels.map((label) => ({
      key: `v:${label}`,
      label,
      value: column.kind === "boolean" ? label === "true" : label,
    }))
    if (column.nullable) list.push({ key: "null", label: "NULL", value: null, special: true })
    if (editor.canDefault) {
      list.push({ key: "default", label: "Default", value: DEFAULT_VALUE, special: true })
    }
    return list
  }, [labels, column, editor.canDefault])

  const current = options.findIndex((option) =>
    option.special
      ? option.value === null
        ? value === null
        : value === undefined || isDefault(value)
      : value !== null && value !== undefined && String(value) === option.label,
  )
  const matching = (prefix: string) =>
    options.findIndex((option) => option.label.toLowerCase().startsWith(prefix.toLowerCase()))
  const [highlight, setHighlight] = useState(() => {
    const seeded = editor.seed ? matching(editor.seed) : -1
    return seeded >= 0 ? seeded : Math.max(current, 0)
  })

  const listRef = useRef<HTMLDivElement>(null)
  const typed = useRef({ text: "", at: 0 })
  const baseId = useId()

  const choose = (index: number, move: EditMove) => {
    const option = options[index]
    if (!option) return
    editor.finish(index === current ? null : { value: option.value }, move)
  }

  useEffect(() => {
    document.getElementById(`${baseId}-${highlight}`)?.scrollIntoView({ block: "nearest" })
  }, [baseId, highlight])

  const { register } = editor
  useEffect(() => {
    // A list has nothing half-typed to lose: moving on closes it.
    register({
      settle: () => {
        editor.finish(null, "none")
        return true
      },
      focus: () => listRef.current?.focus({ preventScroll: true }),
    })
    return () => register(null)
  }, [register, editor])

  return (
    <Popover open>
      <PopoverAnchor asChild>
        <span aria-hidden className="pointer-events-none absolute inset-0" />
      </PopoverAnchor>
      <PopoverContent
        {...EDITOR_PROPS}
        side="bottom"
        align="start"
        sideOffset={2}
        hideWhenDetached
        className="w-auto min-w-(--radix-popover-trigger-width) p-1"
        onOpenAutoFocus={(event) => {
          event.preventDefault()
          listRef.current?.focus({ preventScroll: true })
        }}
        onCloseAutoFocus={(event) => event.preventDefault()}
        onEscapeKeyDown={(event) => {
          event.preventDefault()
          editor.finish(null, "stay")
        }}
        onInteractOutside={(event) => {
          event.preventDefault()
          editor.finish(null, "none")
        }}
      >
        <div
          ref={listRef}
          role="listbox"
          tabIndex={0}
          aria-label={`Set ${column.name}`}
          aria-activedescendant={`${baseId}-${highlight}`}
          className="max-h-64 overflow-y-auto outline-none"
          onKeyDown={(event) => {
            const last = options.length - 1
            if (event.key === "ArrowDown") setHighlight((at) => Math.min(at + 1, last))
            else if (event.key === "ArrowUp") setHighlight((at) => Math.max(at - 1, 0))
            else if (event.key === "Home") setHighlight(0)
            else if (event.key === "End") setHighlight(last)
            else if (event.key === "Enter") choose(highlight, event.shiftKey ? "up" : "down")
            else if (event.key === "Tab") choose(highlight, event.shiftKey ? "left" : "right")
            else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
              // Letters typed close together spell a prefix; a pause starts a new one.
              const now = event.timeStamp
              const text = now - typed.current.at < 700 ? typed.current.text + event.key : event.key
              typed.current = { text, at: now }
              const match = matching(text)
              if (match >= 0) setHighlight(match)
            } else return
            event.preventDefault()
          }}
        >
          {options.map((option, index) => (
            <div
              key={option.key}
              id={`${baseId}-${index}`}
              role="option"
              aria-selected={index === highlight}
              className={cn(
                "flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 font-mono text-xs select-none",
                option.special &&
                  "font-sans text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase",
                index === highlight && "bg-menu-hover",
              )}
              onPointerMove={() => setHighlight(index)}
              onClick={() => choose(index, "stay")}
            >
              <span className="min-w-0 flex-1 truncate">{option.label}</span>
              {index === current && <Check className="size-3.5 text-muted-foreground" />}
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}
