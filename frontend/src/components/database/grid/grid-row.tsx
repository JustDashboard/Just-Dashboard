"use client"

import { memo } from "react"
import { ArrowUpRight, Check, Minus, Warning } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"
import type { InsertedRow, UpdatedRow } from "./change-set"
import { CellEditor } from "./cell-editor"
import type { OrderedColumn } from "./layout"
import { formatCell, isTruncatedValue, type EditValue } from "./values"
import type { CellValue, GridColumn, GridColumnKind, GridRow } from "./types"

/**
 * One row of the grid and the cells in it.
 *
 * Both are memoised on plain values — a row's array, two numbers for the part
 * of a range that crosses it, the index of the active column — so a selection
 * that moves redraws the rows it touched and a single staged edit redraws one
 * cell. Nothing here handles a pointer or a key: the grid listens once, on the
 * body, and reads `data-row` / `data-col` off whatever was hit.
 *
 * A row is mounted every time it scrolls into view, so what it costs to mount
 * is what scrolling costs. That is why a cell is one component over two
 * elements, why its classes are looked up rather than merged, and why the tick
 * in the gutter is a span and not the form checkbox.
 */

/**
 * The legend: which sanctioned `--tag-*` hue a kind of value is drawn in.
 *
 * Text and numbers keep the foreground — they are most of any table, and a
 * grid where everything is coloured is a grid where nothing is. Green, amber
 * and red are left out on purpose: in this grid those three hues mean a row
 * was added, a cell was changed and a row is going, and a boolean drawn green
 * beside an inserted row would be two meanings for one colour.
 */
const KIND_HUE: Record<GridColumnKind, string> = {
  number: "numeric",
  text: "",
  unknown: "",
  boolean: "text-(--tag-violet)",
  datetime: "text-(--tag-cyan)",
  date: "text-(--tag-cyan)",
  time: "text-(--tag-cyan)",
  json: "text-(--tag-blue)",
  array: "text-(--tag-blue)",
  enum: "text-(--tag-pink)",
  uuid: "text-(--tag-slate)",
  binary: "text-(--tag-slate)",
}

const VALUE_TEXT = Object.fromEntries(
  Object.entries(KIND_HUE).map(([kind, hue]) => [
    kind,
    cn("min-w-0 overflow-hidden text-ellipsis whitespace-pre", hue),
  ]),
) as Record<GridColumnKind, string>

/** NULL, the empty string and a pending default are words about a value, not values. */
const ABSENT_TEXT =
  "truncate font-sans text-micro font-medium tracking-[0.06em] text-muted-foreground/70 uppercase"

const LEAD_TEXT = "mr-2 shrink-0 font-sans text-hint text-muted-foreground"

/**
 * A cell's classes for each combination of the six things that change them.
 *
 * State is painted as a wash read from one variable: a row sets `--wash` for
 * the whole row and a cell overrides it for itself. An ordinary cell is
 * transparent and takes the wash as its background; a pinned one needs an
 * opaque ground to hide what scrolls beneath it, so it lays the same wash over
 * that ground — and hover, selection and a pending change look the same on both.
 */
const RIGHT = 1
const PINNED = 2
const PIN_EDGE = 4
const SELECTED = 8
const CHANGED = 16
const STRUCK = 32
const CELL_CLASS = Array.from({ length: 64 }, (_, bits) =>
  cn(
    "relative flex h-full min-w-0 items-center border-r border-b border-hairline px-2 font-mono text-xs focus-ring-inset data-[active]:outline-border-strong group-focus-within/grid:data-[active]:outline-ring",
    bits & PINNED
      ? "sticky z-10 bg-(--jd-grid-ground) [background-image:linear-gradient(var(--wash),var(--wash))]"
      : "bg-(--wash)",
    bits & RIGHT && "justify-end",
    bits & PIN_EDGE && "border-r-border",
    bits & SELECTED
      ? "[--wash:var(--accent)]"
      : bits & CHANGED && "[--wash:color-mix(in_oklab,var(--git-modified)_16%,transparent)]",
    // The corner mark says "changed" without the hue, and survives a selection.
    bits & CHANGED &&
      "before:absolute before:top-0 before:left-0 before:border-4 before:border-transparent before:border-t-(--git-modified) before:border-l-(--git-modified)",
    bits & STRUCK && "text-muted-foreground line-through decoration-(--git-deleted)",
  ),
)

const FOLLOW_CLASS = cn(
  "ml-1 flex size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-control-hover hover:text-foreground",
  rowReveal("row"),
)

const FOLLOW_RIGHT = cn(FOLLOW_CLASS, "order-first mr-auto ml-0")

const PIN_STYLES: React.CSSProperties[] = Array.from({ length: 24 }, (_, ordinal) => ({
  left: `var(--jd-grid-pin-${ordinal})`,
}))

/** The sticky offset of the nth pinned column, which the grid publishes as a variable. */
export function pinStyle(ordinal: number): React.CSSProperties {
  return PIN_STYLES[ordinal] ?? { left: `var(--jd-grid-pin-${ordinal})` }
}

/** A cell kept alive outside the columns being drawn: placed by hand, off to the side. */
const HELD_OUTSIDE: React.CSSProperties = {
  position: "absolute",
  top: 0,
  left: "var(--jd-grid-held-left)",
  width: "var(--jd-grid-held-width)",
}

/**
 * Where a held cell stands. It is the last child of its row whatever column it
 * is, so that scrolling it in and out of the drawn columns changes its style
 * and nothing else — an element that is moved in the document loses focus, and
 * one that is remounted loses what was typed into it. Inside the drawn columns
 * it names its own track (and its row, or the grid would start a second one for
 * it); outside them it is placed by hand.
 */
function heldStyle(track: number, pin: number): React.CSSProperties {
  if (track <= 0) return HELD_OUTSIDE
  const placed: React.CSSProperties = { gridRowStart: 1, gridColumnStart: track }
  return pin >= 0 ? { ...placed, ...pinStyle(pin) } : placed
}

/** The text with every occurrence of the find wrapped in the search-hit mark. */
function marked(text: string, find: string): React.ReactNode {
  const needle = find.trim().toLowerCase()
  if (!needle) return text
  const lower = text.toLowerCase()
  const parts: React.ReactNode[] = []
  let from = 0
  for (let at = lower.indexOf(needle); at >= 0; at = lower.indexOf(needle, from)) {
    if (at > from) parts.push(text.slice(from, at))
    parts.push(
      <mark key={at} className="rounded-sm bg-mark text-inherit">
        {text.slice(at, at + needle.length)}
      </mark>,
    )
    from = at + needle.length
  }
  if (parts.length === 0) return text
  if (from < text.length) parts.push(text.slice(from))
  return parts
}

/**
 * The selector's tick. Decoration, deliberately: which rows are selected is
 * said by `aria-selected` on the row, and the gutter cell is what takes the
 * press. The form checkbox is a button with its own state and half a dozen
 * components under it, which is right for a form and too much to mount forty
 * times a second while a table scrolls.
 */
export function Tick({ state }: { state: boolean | "mixed" }) {
  return (
    <span
      aria-hidden
      data-state={state === true ? "checked" : state === "mixed" ? "mixed" : "unchecked"}
      className="flex size-3.5 shrink-0 items-center justify-center rounded-sm border border-input bg-control text-primary-foreground data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=mixed]:border-primary data-[state=mixed]:bg-primary"
    >
      {state === true && <Check className="size-3" />}
      {state === "mixed" && <Minus className="size-3" />}
    </span>
  )
}

interface GridCellProps {
  column: GridColumn
  /** Position among the visible columns. */
  col: number
  value: EditValue | undefined
  /** The value as it was read, when the cell holds a staged edit. */
  original: CellValue | undefined
  changed: boolean
  selected: boolean
  active: boolean
  editing: boolean
  /** Cannot be edited here: read-only grid, computed column, preview, deleted row. */
  locked: boolean
  /** -1, or which pinned column this is, for its sticky offset. */
  pin: number
  /** The last pinned column, which draws the line the others scroll under. */
  pinEdge: boolean
  struck: boolean
  find: string
  follow: boolean
  /** -1 for a cell in the row's flow; otherwise where a held cell stands (see `heldStyle`). */
  track: number
}

const GridCell = memo(function GridCell({
  column,
  col,
  value,
  original,
  changed,
  selected,
  active,
  editing,
  locked,
  pin,
  pinEdge,
  struck,
  find,
  follow,
  track,
}: GridCellProps) {
  const display = formatCell(value, column)
  const right = column.kind === "number"
  const title = changed
    ? `Was ${original === undefined ? "unset" : formatCell(original, column).text}`
    : display.tone === "default" && column.defaultExpr !== undefined
      ? // What "DEFAULT" will come to, where the column says.
        `Default: ${column.defaultExpr}`
      : display.title
  const target = follow && column.foreignKey && value !== null && value !== undefined
  const bits =
    (right ? RIGHT : 0) |
    (pin >= 0 ? PINNED : 0) |
    (pinEdge ? PIN_EDGE : 0) |
    (selected ? SELECTED : 0) |
    (changed ? CHANGED : 0) |
    (struck ? STRUCK : 0)

  return (
    <div
      role="gridcell"
      aria-colindex={col + 2}
      aria-selected={selected || active}
      aria-readonly={locked || undefined}
      data-col={col}
      data-active={active ? "" : undefined}
      data-changed={changed ? "" : undefined}
      tabIndex={active && !editing ? 0 : -1}
      title={title}
      style={track >= 0 ? heldStyle(track, pin) : pin >= 0 ? pinStyle(pin) : undefined}
      className={CELL_CLASS[bits]}
    >
      {editing ? (
        <CellEditor column={column} value={value} />
      ) : (
        <>
          {display.lead && <span className={LEAD_TEXT}>{display.lead}</span>}
          <span className={display.tone === "value" ? VALUE_TEXT[column.kind] : ABSENT_TEXT}>
            {find && display.tone === "value" ? marked(display.text, find) : display.text}
          </span>
          {target && (
            <button
              type="button"
              tabIndex={-1}
              data-follow=""
              aria-label={`Open the ${column.foreignKey?.table} row this points to`}
              className={right ? FOLLOW_RIGHT : FOLLOW_CLASS}
            >
              <ArrowUpRight className="size-3" />
            </button>
          )}
        </>
      )}
    </div>
  )
})

export type RowState = "none" | "inserted" | "updated" | "deleted"

const ROW = "group/row absolute left-0 grid h-(--jd-grid-row) w-(--jd-grid-width) min-w-full"

const ROW_CLASS: Record<"plain" | "selected" | "inserted" | "deleted", string> = {
  plain: `${ROW} [--wash:transparent] hover:[--wash:var(--row-hover)]`,
  selected: `${ROW} [--wash:var(--accent)]`,
  inserted: `${ROW} [--wash:color-mix(in_oklab,var(--git-added)_11%,transparent)]`,
  deleted: `${ROW} [--wash:color-mix(in_oklab,var(--git-deleted)_11%,transparent)]`,
}

const GUTTER =
  "sticky left-0 z-10 flex h-full min-w-0 cursor-default items-center gap-1.5 border-r border-b border-hairline bg-(--jd-grid-ground) [background-image:linear-gradient(var(--wash),var(--wash))] px-2 select-none"

/** The change glyph beside a row number: the same three signs a diff uses. */
const STATE_MARK: Record<Exclude<RowState, "none">, { sign: string; word: string; hue: string }> = {
  inserted: { sign: "+", word: "New row", hue: "font-mono font-semibold text-(--git-added)" },
  updated: { sign: "~", word: "Edited", hue: "font-mono font-semibold text-(--git-modified)" },
  deleted: {
    sign: "−",
    word: "Marked for deletion",
    hue: "font-mono font-semibold text-(--git-deleted)",
  },
}

export interface GridRowProps {
  id: string
  /** Position in the grid as drawn. */
  index: number
  /** The row number, or empty for a row that does not exist yet. */
  label: string
  top: number
  /**
   * The column tracks, as `grid-template-columns`. Handed to each row rather
   * than inherited from a variable on the scroller: a custom property that
   * changes on an ancestor makes the browser restyle every element under it,
   * and this one changes whenever the drawn columns do.
   */
  template: string
  /** The row as read. Null for an inserted row. */
  values: GridRow | null
  insert: InsertedRow | undefined
  update: UpdatedRow | undefined
  deleted: boolean
  /** Source column index → whole size in bytes, for the cells the server cut. */
  clipped: ReadonlyMap<number, number> | undefined
  /** The row's key arrived cut, so nothing in the row can be changed. */
  keyCut: boolean
  /** Why the server refused this row's change, when it did. */
  error: string | undefined
  pinned: readonly OrderedColumn[]
  cols: readonly OrderedColumn[]
  /**
   * A column whose cell must not be unmounted or moved: the one being edited,
   * or the active one when it has scrolled out of the drawn columns.
   */
  held: OrderedColumn | null
  /** The grid track the held cell stands in, or 0 when it is outside the drawn columns. */
  heldTrack: number
  /** Chosen through the selector column. */
  selected: boolean
  /** The columns of the cell range that cross this row, or -1 and -1. */
  spanStart: number
  spanEnd: number
  activeCol: number
  editingCol: number
  editable: boolean
  selectable: boolean
  find: string
  follow: boolean
}

export const GridRowView = memo(function GridRowView(props: GridRowProps) {
  const { index, label, insert, update, deleted, clipped, error } = props
  const state: RowState = insert ? "inserted" : deleted ? "deleted" : update ? "updated" : "none"
  const mark = state === "none" ? null : STATE_MARK[state]
  const wash = props.selected
    ? "selected"
    : state === "inserted" || state === "deleted"
      ? state
      : "plain"

  const held = props.held?.column.key
  const cell = (entry: OrderedColumn, pin: number, pinEdge: boolean, track: number) => {
    const { column, source } = entry
    const edited = update?.values
    const changed = edited !== undefined && column.key in edited
    const read = props.values?.[source] ?? null
    const value = insert ? insert.values[column.key] : changed ? edited[column.key] : read
    const preview = clipped?.has(source) || (typeof read === "string" && isTruncatedValue(read))
    return (
      <GridCell
        key={track >= 0 ? `held:${column.key}` : column.key}
        column={column}
        col={entry.index}
        value={value}
        original={changed ? read : undefined}
        changed={changed}
        selected={entry.index >= props.spanStart && entry.index <= props.spanEnd}
        active={entry.index === props.activeCol}
        editing={entry.index === props.editingCol}
        locked={
          !props.editable ||
          deleted ||
          props.keyCut ||
          column.generated === true ||
          column.editable === false ||
          (!insert && preview)
        }
        pin={pin}
        pinEdge={pinEdge}
        struck={deleted}
        find={props.find}
        follow={props.follow}
        track={track}
      />
    )
  }

  const lastPin = props.pinned.length - 1
  const heldPin = props.held ? props.pinned.indexOf(props.held) : -1

  return (
    <div
      role="row"
      aria-rowindex={index + 2}
      aria-selected={props.selected}
      data-row={index}
      data-state={state}
      style={{ top: props.top, gridTemplateColumns: props.template }}
      className={ROW_CLASS[wash]}
    >
      <div role="rowheader" aria-colindex={1} data-gutter="" title={error} className={GUTTER}>
        {props.selectable && <Tick state={props.selected} />}
        <span className="numeric ml-auto flex min-w-0 items-center gap-1 text-hint text-muted-foreground">
          {error ? (
            <Warning aria-label={error} className="size-3 shrink-0 text-destructive" />
          ) : (
            mark && (
              <span className={mark.hue}>
                <span aria-hidden>{mark.sign}</span>
                <span className="sr-only">{mark.word}</span>
              </span>
            )
          )}
          <span className="truncate">{label}</span>
        </span>
      </div>
      {props.pinned.map((entry, i) =>
        entry.column.key === held ? null : cell(entry, i, i === lastPin, -1),
      )}
      <div role="presentation" />
      {props.cols.map((entry) => (entry.column.key === held ? null : cell(entry, -1, false, -1)))}
      {props.held &&
        cell(props.held, heldPin, heldPin >= 0 && heldPin === lastPin, props.heldTrack)}
    </div>
  )
})

/**
 * Rows that are on their way: the header stays, and under it bars in the
 * columns' own tracks, so the grid that arrives lands where its placeholder was.
 */
export function GridSkeletonRows({
  rows,
  leading,
  trailing,
  template,
}: {
  rows: number
  /** Tracks before the one that stands in for scrolled-past columns: the gutter and the pinned. */
  leading: number
  /** Tracks after it. */
  trailing: number
  template: string
}) {
  const bar = (row: number, col: number) => (
    <div key={col} className="flex items-center border-r border-b border-hairline px-2">
      {/* Staggered so the placeholder reads as values of different lengths, not a wall. */}
      <Skeleton
        className="h-3"
        style={{ width: `${col === 0 ? 100 : 35 + ((row * 7 + col * 13) % 50)}%` }}
      />
    </div>
  )
  return (
    <div role="presentation" aria-hidden>
      {Array.from({ length: rows }, (_, row) => (
        <div
          key={row}
          style={{ gridTemplateColumns: template }}
          className="grid h-(--jd-grid-row) w-(--jd-grid-width) min-w-full"
        >
          {Array.from({ length: leading }, (_, col) => bar(row, col))}
          <div />
          {Array.from({ length: trailing }, (_, col) => bar(row, leading + col))}
        </div>
      ))}
    </div>
  )
}
