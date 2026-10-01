"use client"

import { memo } from "react"
import { ArrowDown, ArrowUp, ChevronDown, Key, Linked } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { cn } from "@/lib/utils"
import { pinStyle, Tick } from "./grid-row"
import type { OrderedColumn } from "./layout"
import { sortState } from "./sort"
import type { GridColumn, GridSort } from "./types"

/**
 * The header row: what each column is called, what it holds, and how the grid
 * is ordered by it.
 *
 * A column name is a quiet label (§12 of the design system: hint size, medium,
 * muted), its type a mono `Tag` beside it, and the key and foreign-key marks
 * the two glyphs that are part of what a column *is*. Like the body it handles
 * no pointer itself — the grid reads `data-hcol` and `data-resize` off the
 * element that was pressed — so the only thing each cell owns is its menu.
 */

/** What a column's menu can ask for. Stable across renders, so the header memoises. */
export interface ColumnActions {
  /** `desc` null clears the column's sort. `additive` keeps the other keys. */
  sort: (column: GridColumn, desc: boolean | null, additive: boolean) => void
  pin: (column: GridColumn, pinned: boolean) => void
  hide: (column: GridColumn) => void
  fit: (column: GridColumn) => void
  resetWidth: (column: GridColumn) => void
  select: (index: number) => void
  copyName: (column: GridColumn) => void
  setMenu: (index: number | null) => void
  /** Puts focus back on the grid's active cell once a menu has closed. */
  restoreFocus: () => void
}

const HEAD =
  "group relative flex h-full min-w-0 items-center gap-1.5 border-r border-hairline bg-(--jd-grid-ground) px-2 text-hint font-medium text-muted-foreground select-none focus-ring-inset data-[active]:outline-border-strong group-focus-within/grid:data-[active]:outline-ring"

function ColumnFacts({ column }: { column: GridColumn }) {
  const facts: [string, string][] = []
  if (column.primaryKey) facts.push(["Key", "primary"])
  if (column.foreignKey) {
    const { schema, table, column: target } = column.foreignKey
    facts.push(["References", `${schema ? `${schema}.` : ""}${table}.${target}`])
  }
  if (column.nullable === false) facts.push(["Null", "not allowed"])
  if (column.defaultExpr !== undefined) facts.push(["Default", column.defaultExpr])
  if (column.generated) facts.push(["Computed", "by the database"])
  if (column.comment) facts.push(["Comment", column.comment])
  return (
    <div className="max-w-72 px-2 pt-2 pb-1">
      <div className="flex min-w-0 items-center gap-2">
        <span className="min-w-0 truncate font-mono text-body font-medium">{column.name}</span>
        <Tag mono className="min-w-0 shrink">
          <span className="truncate">{column.typeName || column.kind}</span>
        </Tag>
      </div>
      {facts.length > 0 && (
        <dl className="mt-1.5 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-hint">
          {facts.map(([label, value]) => (
            <div key={label} className="contents">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="min-w-0 font-mono break-words">{value}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  )
}

function ColumnMenu({
  entry,
  sort,
  sortable,
  customWidth,
  open,
  actions,
}: {
  entry: OrderedColumn
  sort: GridSort
  sortable: boolean
  customWidth: boolean
  open: boolean
  actions: ColumnActions
}) {
  const { column, index, pinned } = entry
  const state = sortState(sort, column.key)
  // With another column already sorted, the same two directions can be added
  // as a further key instead of replacing it.
  const others = sort.some((key) => key.column !== column.key)
  return (
    <DropdownMenu open={open} onOpenChange={(next) => actions.setMenu(next ? index : null)}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          tabIndex={-1}
          data-column-menu=""
          aria-label={`${column.name} column options`}
          className={cn(
            "flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-control-hover hover:text-foreground",
            rowReveal(),
          )}
        >
          <ChevronDown className="size-3" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="start"
        className="min-w-52"
        // The trigger is a mouse affordance and not a tab stop; focus belongs
        // back on the cell the reader was on.
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          actions.restoreFocus()
        }}
      >
        <ColumnFacts column={column} />
        <DropdownMenuSeparator />
        {sortable && (
          <>
            <DropdownMenuItem onSelect={() => actions.sort(column, false, false)}>
              Sort ascending
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => actions.sort(column, true, false)}>
              Sort descending
            </DropdownMenuItem>
            {others && !state && (
              <>
                <DropdownMenuItem onSelect={() => actions.sort(column, false, true)}>
                  Then ascending
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => actions.sort(column, true, true)}>
                  Then descending
                </DropdownMenuItem>
              </>
            )}
            {state && (
              <DropdownMenuItem onSelect={() => actions.sort(column, null, true)}>
                Clear sort
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
          </>
        )}
        <DropdownMenuItem onSelect={() => actions.pin(column, !pinned)}>
          {pinned ? "Unpin" : "Pin left"}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.hide(column)}>Hide</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.fit(column)}>Fit to content</DropdownMenuItem>
        {customWidth && (
          <DropdownMenuItem onSelect={() => actions.resetWidth(column)}>
            Reset width
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => actions.select(index)}>Select column</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.copyName(column)}>Copy name</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export interface GridHeaderProps {
  /** The column tracks, the same string every row is given. */
  template: string
  pinned: readonly OrderedColumn[]
  cols: readonly OrderedColumn[]
  sort: GridSort
  sortable: boolean
  /** Keys of the columns the reader has dragged to a width. */
  customWidths: Readonly<Record<string, number>>
  /** The active column when the header row holds the active cell, else -1. */
  activeCol: number
  /** The column whose menu is open, or null. */
  menu: number | null
  /** The column being dragged to a new place, or null. */
  moving: string | null
  selectable: boolean
  allRows: boolean | "mixed"
  actions: ColumnActions
}

export const GridHeader = memo(function GridHeader({
  template,
  pinned,
  cols,
  sort,
  sortable,
  customWidths,
  activeCol,
  menu,
  moving,
  selectable,
  allRows,
  actions,
}: GridHeaderProps) {
  const head = (entry: OrderedColumn, pin: number, pinEdge: boolean) => {
    const { column, index } = entry
    const state = sortState(sort, column.key)
    const active = index === activeCol
    return (
      <div
        key={column.key}
        role="columnheader"
        aria-colindex={index + 2}
        aria-sort={
          state ? (state.desc ? "descending" : "ascending") : sortable ? "none" : undefined
        }
        data-hcol={index}
        data-active={active ? "" : undefined}
        tabIndex={active ? 0 : -1}
        style={pin >= 0 ? pinStyle(pin) : undefined}
        className={cn(
          HEAD,
          sortable && "cursor-pointer",
          pin >= 0 && "sticky z-10",
          pinEdge && "border-r-border",
          moving === column.key && "opacity-50",
        )}
      >
        {column.primaryKey && (
          <Key aria-label="Primary key" className="size-3 shrink-0 text-chart-2" />
        )}
        {column.foreignKey && (
          <Linked
            aria-label={`References ${column.foreignKey.table}`}
            className="size-3 shrink-0 text-chart-1"
          />
        )}
        {/* The pixel of padding is for the last glyph: a box exactly as wide as
            its text clips the ink of a letter that overhangs its advance. */}
        <span className="min-w-0 truncate pr-px">{column.name}</span>
        {column.typeName && (
          // The type gives way before the name does: a column is found by what
          // it is called, and `timestamp with time zone` can afford to lose its tail.
          <Tag mono className="min-w-0 shrink-[999]">
            <span className="truncate">{column.typeName}</span>
          </Tag>
        )}
        <span className="ml-auto flex shrink-0 items-center gap-0.5">
          {state && (
            <span className="flex items-center text-foreground">
              {state.desc ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />}
              {state.order !== null && <span className="numeric text-micro">{state.order}</span>}
            </span>
          )}
          <ColumnMenu
            entry={entry}
            sort={sort}
            sortable={sortable}
            customWidth={column.key in customWidths}
            open={menu === index}
            actions={actions}
          />
        </span>
        {/* The drag strip sits across the column's right edge, half on each side of the rule. */}
        <span
          aria-hidden
          data-resize={index}
          className="absolute inset-y-0 -right-1 z-20 w-2 cursor-col-resize after:absolute after:inset-y-1.5 after:left-1/2 after:w-px after:-translate-x-1/2 hover:after:bg-border-strong"
        />
      </div>
    )
  }

  return (
    <div
      role="row"
      aria-rowindex={1}
      style={{ gridTemplateColumns: template }}
      className="grid h-9 w-(--jd-grid-width) min-w-full border-b border-hairline bg-(--jd-grid-ground)"
    >
      <div
        role="columnheader"
        aria-colindex={1}
        data-select-all={selectable ? "" : undefined}
        aria-label={selectable ? "Select every row on this page" : undefined}
        className="sticky left-0 z-10 flex h-full cursor-default items-center border-r border-hairline bg-(--jd-grid-ground) px-2"
      >
        {selectable ? <Tick state={allRows} /> : <span className="sr-only">Row</span>}
      </div>
      {pinned.map((entry, i) => head(entry, i, i === pinned.length - 1))}
      <div role="presentation" />
      {cols.map((entry) => head(entry, -1, false))}
    </div>
  )
})
