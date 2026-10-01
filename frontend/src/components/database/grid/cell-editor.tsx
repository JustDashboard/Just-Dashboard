"use client"

import { createContext, useContext, useEffect, useId, useMemo, useRef, useState } from "react"
import { Check } from "@/components/icons"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover"
import { cn } from "@/lib/utils"
import { numberSpec } from "./kinds"
import {
  DEFAULT_VALUE,
  editText,
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
  /**
   * Closes the editor. `result` is the value to stage, or null when nothing
   * changed. `fill` carries the typed text for every other cell of the range.
   */
  finish: (result: { value: EditValue } | null, move: EditMove, fill?: string) => void
  /** Lets the grid ask the open editor to settle before it moves somewhere else. */
  register: (handle: { settle: () => boolean } | null) => void
}

export const GridEditorContext = createContext<GridEditorApi | null>(null)

/** What marks an element as part of an editor, so the grid leaves its keys and clicks alone. */
export const EDITOR_ATTR = { "data-grid-editor": "" } as const

function columnHint(column: GridColumn): string {
  switch (column.kind) {
    case "number": {
      const spec = numberSpec(column.typeName)
      if (spec.class === "integer") return `${column.typeName} · ${spec.min} to ${spec.max}`
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
      return column.typeName
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
  const structured =
    column.kind === "json" || (column.kind === "array" && typeof value === "object")
  const [expanded, setExpanded] = useState(
    () => editor.expanded || structured || initial.includes("\n") || initial.length > LONG_TEXT,
  )

  const parsed = useMemo(() => parseInput(text, column), [text, column])
  // Opening a cell and leaving it is not an edit: a NULL that was only looked
  // at must not come back as an empty string.
  const dirty = editor.seed !== null || text !== initial
  const starting = value === null || value === undefined || isDefault(value)

  const settle = (move: EditMove, fill?: boolean): boolean => {
    if (!dirty) {
      editor.finish(null, move)
      return true
    }
    if (!parsed.ok) return false
    editor.finish({ value: parsed.value }, move, fill ? text : undefined)
    return true
  }
  const settleRef = useRef(settle)
  useEffect(() => {
    settleRef.current = settle
  })
  const { register } = editor
  useEffect(() => {
    register({ settle: () => settleRef.current("none") })
    return () => register(null)
  }, [register])

  const shared = {
    column,
    text,
    setText,
    error: dirty && !parsed.ok ? parsed.error : null,
    settle,
    setNull: column.nullable ? () => editor.finish({ value: null }, "stay") : null,
    setDefault:
      column.defaultExpr !== undefined
        ? () => editor.finish({ value: DEFAULT_VALUE }, "stay")
        : null,
    cancel: () => editor.finish(null, "stay"),
  }

  if (expanded) return <AreaEditor {...shared} />
  return (
    <InlineEditor
      {...shared}
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
  setNull: (() => void) | null
  setDefault: (() => void) | null
  cancel: () => void
}

/** Keeps a press on a strip button from taking focus out of the field it belongs to. */
const keepFocus = (event: React.PointerEvent) => event.preventDefault()

function ValueButtons({ setNull, setDefault }: Pick<TextEditorShared, "setNull" | "setDefault">) {
  return (
    <>
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

/** Ctrl+Alt+N and Ctrl+Alt+D, wherever an editor has the keyboard. */
function valueShortcut(
  event: React.KeyboardEvent,
  { setNull, setDefault }: Pick<TextEditorShared, "setNull" | "setDefault">,
): boolean {
  if (!(event.ctrlKey || event.metaKey) || !event.altKey) return false
  const key = event.key.toLowerCase()
  if (key === "n" && setNull) setNull()
  else if (key === "d" && setDefault) setDefault()
  else return false
  event.preventDefault()
  return true
}

function InlineEditor({
  column,
  text,
  setText,
  error,
  settle,
  setNull,
  setDefault,
  cancel,
  caret,
  canFill,
  placeholder,
  onExpand,
}: TextEditorShared & {
  caret: "select" | "end"
  canFill: boolean
  placeholder: string
  onExpand: (() => void) | null
}) {
  const ref = useRef<HTMLInputElement>(null)
  const hintId = useId()
  // The editor is unmounted by the same call that settles it, and the blur that
  // follows must not settle it a second time.
  const closed = useRef(false)

  useEffect(() => {
    const input = ref.current
    if (!input) return
    input.focus({ preventScroll: true })
    if (caret === "select") input.select()
    else input.setSelectionRange(input.value.length, input.value.length)
  }, [caret])

  const finish = (move: EditMove, fill?: boolean) => {
    if (settle(move, fill)) closed.current = true
  }

  return (
    <Popover open>
      <PopoverAnchor asChild>
        <input
          {...EDITOR_ATTR}
          ref={ref}
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
            if (valueShortcut(event, { setNull, setDefault })) {
              closed.current = true
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
        {...EDITOR_ATTR}
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
        <ValueButtons setNull={setNull} setDefault={setDefault} />
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
  setDefault,
  cancel,
}: TextEditorShared) {
  const ref = useRef<HTMLTextAreaElement>(null)
  const hintId = useId()
  const json = column.kind === "json" || column.kind === "array"

  const format = () => {
    try {
      setText(JSON.stringify(JSON.parse(text), null, 2))
    } catch {
      // The error under the field already says why it will not format.
    }
  }

  return (
    <Popover open>
      <PopoverAnchor asChild>
        <span aria-hidden className="pointer-events-none absolute inset-0" />
      </PopoverAnchor>
      <PopoverContent
        {...EDITOR_ATTR}
        side="bottom"
        align="start"
        sideOffset={2}
        hideWhenDetached
        collisionPadding={8}
        className="flex w-[min(36rem,calc(100vw-1rem))] flex-col gap-0 p-0"
        onOpenAutoFocus={(event) => {
          event.preventDefault()
          const area = ref.current
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
          if (valueShortcut(event, { setNull, setDefault })) return
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
          ref={ref}
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
          <ValueButtons setNull={setNull} setDefault={setDefault} />
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
    if (column.defaultExpr !== undefined) {
      list.push({ key: "default", label: "Default", value: DEFAULT_VALUE, special: true })
    }
    return list
  }, [labels, column])

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
    })
    return () => register(null)
  }, [register, editor])

  return (
    <Popover open>
      <PopoverAnchor asChild>
        <span aria-hidden className="pointer-events-none absolute inset-0" />
      </PopoverAnchor>
      <PopoverContent
        {...EDITOR_ATTR}
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
