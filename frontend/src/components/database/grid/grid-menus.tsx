"use client"

import { useState } from "react"
import { ChipCount } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import {
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
} from "@/components/ui/context-menu"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { displayOrder, EMPTY_LAYOUT, setColumnHidden } from "./layout"
import type { RowState } from "./grid-row"
import type { GridColumn, GridLayout } from "./types"

/** The forms a selection can be copied in. SQL is the owner's: only a server knows the dialect. */
export type CopyFormat = "tsv" | "tsv-header" | "csv" | "json" | "markdown"

/** What the cell under the menu is, and what can be done to it. Decided by the grid. */
export interface CellMenuTarget {
  column: GridColumn
  /**
   * How many cells the cell verbs would act on: the range or the ticked rows
   * when the menu was opened inside them, otherwise the one cell under it.
   */
  cells: number
  /** How many rows the row verbs would act on. */
  rows: number
  isNull: boolean
  editable: boolean
  /** Whether any of those cells can take NULL, the empty string, its default. */
  canNull: boolean
  canEmpty: boolean
  canDefault: boolean
  changed: boolean
  rowState: RowState
  canInsert: boolean
  canDelete: boolean
  canFilter: boolean
  canOpen: boolean
  canFollow: boolean
  canCopySQL: boolean
  canPaste: boolean
}

export interface CellMenuActions {
  copy: (format: CopyFormat) => void
  copyRowJSON: () => void
  copySQL: () => void
  paste: () => void
  filter: (op: "eq" | "ne" | "is_null" | "not_null") => void
  follow: () => void
  edit: () => void
  setNull: () => void
  setEmpty: () => void
  setDefault: () => void
  revertCell: () => void
  revertRows: () => void
  openRow: () => void
  duplicate: () => void
  deleteRows: () => void
}

/**
 * What a right-click on a cell offers.
 *
 * Grouped the way the cell is thought about: take the value somewhere, narrow
 * the table by it, change it, then the row it belongs to. A verb the role or
 * the table cannot use is not drawn rather than greyed out.
 */
export function CellMenuItems({
  target,
  actions,
}: {
  target: CellMenuTarget
  actions: CellMenuActions
}) {
  const many = target.rows > 1
  const gone = target.rowState === "deleted"
  const pending = target.rowState !== "none"
  // A verb that reaches past the cell under the pointer says how far.
  const block = target.cells > 1 ? ` in ${target.cells.toLocaleString("en-US")} cells` : ""
  const writes = target.canNull || target.canEmpty || target.canDefault
  return (
    <>
      <ContextMenuItem onSelect={() => actions.copy("tsv")}>
        {target.cells > 1 ? "Copy" : "Copy value"}
        <ContextMenuShortcut>Ctrl C</ContextMenuShortcut>
      </ContextMenuItem>
      <ContextMenuSub>
        <ContextMenuSubTrigger>Copy as</ContextMenuSubTrigger>
        <ContextMenuSubContent>
          <ContextMenuItem onSelect={() => actions.copy("tsv-header")}>
            Tab-separated, with headers
          </ContextMenuItem>
          <ContextMenuItem onSelect={() => actions.copy("csv")}>CSV</ContextMenuItem>
          <ContextMenuItem onSelect={() => actions.copy("json")}>JSON</ContextMenuItem>
          <ContextMenuItem onSelect={() => actions.copy("markdown")}>
            Markdown table
          </ContextMenuItem>
          {target.canCopySQL && (
            <ContextMenuItem onSelect={actions.copySQL}>SQL INSERT</ContextMenuItem>
          )}
        </ContextMenuSubContent>
      </ContextMenuSub>
      <ContextMenuItem onSelect={actions.copyRowJSON}>
        {many ? "Copy rows as JSON" : "Copy row as JSON"}
      </ContextMenuItem>
      {target.canPaste && (
        <ContextMenuItem onSelect={actions.paste}>
          Paste
          <ContextMenuShortcut>Ctrl V</ContextMenuShortcut>
        </ContextMenuItem>
      )}

      {(target.canFilter || target.canFollow) && <ContextMenuSeparator />}
      {target.canFilter && (
        <>
          <ContextMenuItem onSelect={() => actions.filter(target.isNull ? "is_null" : "eq")}>
            Filter by this value
          </ContextMenuItem>
          <ContextMenuItem onSelect={() => actions.filter(target.isNull ? "not_null" : "ne")}>
            Exclude this value
          </ContextMenuItem>
        </>
      )}
      {target.canFollow && (
        <ContextMenuItem onSelect={actions.follow}>
          Open the {target.column.foreignKey?.table} row
        </ContextMenuItem>
      )}

      {(target.editable || writes || target.changed) && <ContextMenuSeparator />}
      {target.editable && (
        <ContextMenuItem onSelect={actions.edit}>
          Edit
          <ContextMenuShortcut>Enter</ContextMenuShortcut>
        </ContextMenuItem>
      )}
      {target.canNull && (
        <ContextMenuItem onSelect={actions.setNull}>Set NULL{block}</ContextMenuItem>
      )}
      {target.canEmpty && (
        <ContextMenuItem onSelect={actions.setEmpty}>Set empty string{block}</ContextMenuItem>
      )}
      {target.canDefault && (
        <ContextMenuItem onSelect={actions.setDefault}>Set default{block}</ContextMenuItem>
      )}
      {target.changed && (
        <ContextMenuItem onSelect={actions.revertCell}>Revert cell</ContextMenuItem>
      )}

      {(target.canOpen || target.canInsert || target.canDelete || pending) && (
        <ContextMenuSeparator />
      )}
      {target.canOpen && (
        <ContextMenuItem onSelect={actions.openRow}>
          Open row
          <ContextMenuShortcut>Space</ContextMenuShortcut>
        </ContextMenuItem>
      )}
      {target.canInsert && !gone && (
        <ContextMenuItem onSelect={actions.duplicate}>
          {many ? `Duplicate ${target.rows} rows` : "Duplicate row"}
        </ContextMenuItem>
      )}
      {pending && (
        <ContextMenuItem onSelect={actions.revertRows}>
          {gone ? (many ? "Restore rows" : "Restore row") : many ? "Revert rows" : "Revert row"}
        </ContextMenuItem>
      )}
      {target.canDelete && !gone && (
        <ContextMenuItem variant="destructive" onSelect={actions.deleteRows}>
          {many ? `Delete ${target.rows} rows` : "Delete row"}
          <ContextMenuShortcut>Del</ContextMenuShortcut>
        </ContextMenuItem>
      )}
    </>
  )
}

/** Past this many columns the menu grows a box to find one in. */
const SEARCH_FROM = 12

/**
 * Which columns are shown.
 *
 * Exported for the owner's toolbar: the grid draws one in its own strip, and a
 * page that lays its commands out differently puts this where it wants it and
 * hands it the same layout the grid has.
 */
export function GridColumnsMenu({
  columns,
  layout,
  onLayoutChange,
}: {
  columns: readonly GridColumn[]
  layout: GridLayout
  onLayoutChange: (layout: GridLayout) => void
}) {
  const [query, setQuery] = useState("")
  const hidden = new Set(layout.hidden)
  const byKey = new Map(columns.map((column) => [column.key, column]))
  const ordered = displayOrder(columns, layout).flatMap((key) => byKey.get(key) ?? [])
  const needle = query.trim().toLowerCase()
  const shown = needle
    ? ordered.filter((column) => column.name.toLowerCase().includes(needle))
    : ordered
  const visible = ordered.filter((column) => !hidden.has(column.key)).length
  const arranged =
    layout.order.length + layout.hidden.length + layout.pinned.length > 0 ||
    Object.keys(layout.widths).length > 0

  return (
    <DropdownMenu onOpenChange={(open) => !open && setQuery("")}>
      <DropdownMenuTrigger asChild>
        <Button type="button" size="xs" variant="outline">
          Columns
          {visible < ordered.length && (
            <ChipCount>
              {visible}/{ordered.length}
            </ChipCount>
          )}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        {ordered.length > SEARCH_FROM && (
          <div className="p-1 pb-2">
            <Input
              value={query}
              placeholder="Find a column"
              aria-label="Find a column"
              className="h-7 sm:h-7"
              onChange={(event) => setQuery(event.target.value)}
              // The menu reads typed letters as type-ahead; in the box they are text.
              onKeyDown={(event) => event.key !== "Escape" && event.stopPropagation()}
            />
          </div>
        )}
        <div className="max-h-72 overflow-y-auto">
          {shown.map((column) => (
            <DropdownMenuCheckboxItem
              key={column.key}
              checked={!hidden.has(column.key)}
              // Choosing several in a row is the point; the menu stays open.
              onSelect={(event) => event.preventDefault()}
              onCheckedChange={(checked) =>
                onLayoutChange(setColumnHidden(layout, column.key, !checked))
              }
            >
              <span className="min-w-0 flex-1 truncate font-mono text-xs">{column.name}</span>
              <span className="max-w-24 shrink-0 truncate text-hint text-muted-foreground">
                {column.typeName}
              </span>
            </DropdownMenuCheckboxItem>
          ))}
          {shown.length === 0 && (
            <p className="px-2 py-3 text-center text-hint text-muted-foreground">
              No column is called that
            </p>
          )}
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          disabled={layout.hidden.length === 0}
          onSelect={() => onLayoutChange({ ...layout, hidden: [] })}
        >
          Show all
        </DropdownMenuItem>
        <DropdownMenuItem disabled={!arranged} onSelect={() => onLayoutChange(EMPTY_LAYOUT)}>
          Reset arrangement
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
