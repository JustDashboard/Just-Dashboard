"use client"

import { useLayoutEffect, useRef } from "react"
import type { ChangeSetController } from "./change-set"
import type { OrderedColumn } from "./layout"
import type { GridModel } from "./model"
import type { Slice } from "./window"
import type {
  GridCellRef,
  GridColumn,
  GridFilterRequest,
  GridForeignKeyTarget,
  GridLayout,
  GridRow,
  GridRowRef,
  GridSelection,
  GridSelectionData,
  GridSort,
} from "./types"

/** The cell being edited, and how its editor was opened. */
export interface Editing {
  rowId: string
  column: string
  seed: string | null
  caret: "select" | "end"
  expanded: boolean
  canFill: boolean
}

/**
 * Everything a handler needs to know about the grid as it stands.
 *
 * The grid's handlers are created once and must not be recreated when a cell is
 * selected or a row scrolls in — a new handler is a new prop, and a new prop is
 * a row redrawn. So they read the present from here instead of closing over it.
 */
export interface GridLive {
  model: GridModel
  /** Rows drawn: the server's, then the staged ones. */
  count: number
  ordered: readonly OrderedColumn[]
  widths: readonly number[]
  /** Running totals of `widths`. */
  offsets: readonly number[]
  /** The same for the unpinned columns alone. */
  scrollOffsets: readonly number[]
  gutter: number
  pinnedCount: number
  pinnedWidth: number
  rowHeight: number
  rowWin: Slice
  colWin: Slice
  size: { width: number; height: number }
  sel: GridSelection
  setSelection: (next: GridSelection) => void
  sort: GridSort
  setSort: (next: GridSort) => void
  sortable: boolean
  layout: GridLayout
  setLayout: (next: GridLayout) => void
  changeSet: ChangeSetController | undefined
  canEdit: boolean
  canInsert: boolean
  canDelete: boolean
  editing: Editing | null
  /** The cell the context menu was opened on. */
  menu: GridCellRef | null
  /** The column being dragged to a new place, and where it would land. */
  moving: { key: string; before: string | null; x: number } | null
  columns: readonly GridColumn[]
  rows: readonly GridRow[]
  onOpenRow: ((row: GridRowRef) => void) | undefined
  onFilter: ((request: GridFilterRequest) => void) | undefined
  onFollowForeignKey: ((target: GridForeignKeyTarget) => void) | undefined
  onCopySQL: ((data: GridSelectionData) => void) | undefined
  findable: boolean
}

export type GridLiveRef = { readonly current: GridLive }

/** What to do once the next render has landed: where to scroll, and whether to take the keyboard. */
export type AfterRender = { reveal: GridCellRef | null; focus: boolean }

/** The latest render's values, for handlers that are created once. */
export function useLive<T>(value: T): { readonly current: T } {
  const ref = useRef(value)
  useLayoutEffect(() => {
    ref.current = value
  })
  return ref
}
