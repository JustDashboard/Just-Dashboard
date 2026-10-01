"use client"

import { useCallback } from "react"
import { HEADER_HEIGHT, moveColumn, setColumnWidth } from "./layout"
import type { GridLiveRef } from "./live"
import { tickedRows } from "./model"
import {
  collapse,
  HEADER_ROW,
  isRange,
  moveCell,
  rangeOf,
  selectAll,
  selectColumn,
  toggleRow,
  toggleRowRange,
  type Move,
} from "./selection"
import type { GridCellRef, GridColumn } from "./types"

/**
 * The grid's keys.
 *
 * One handler on the scroller, for whichever cell has focus. Movement is the
 * spreadsheet's — arrows, Home and End, a page at a time, Tab along the row and
 * on to the next — and Shift grows the range instead of moving it. Enter, F2 or
 * simply typing opens the cell; Space opens the row.
 *
 * Tab is the one key a grid can turn into a trap, so it is handed back to the
 * browser twice over: past the last cell, and after an Escape that had nothing
 * left to clear.
 */
export function useGridKeyboard({
  liveRef,
  tabOutRef,
  findRef,
  fromGrid,
  moveTo,
  startEdit,
  openRow,
  sortBy,
  setNull,
  setDefault,
  deleteRows,
  clearCells,
}: {
  liveRef: GridLiveRef
  /** Set by an Escape with nothing to clear: the next Tab leaves the grid. */
  tabOutRef: { current: boolean }
  findRef: { readonly current: HTMLInputElement | null }
  fromGrid: (target: EventTarget | null) => boolean
  moveTo: (cell: GridCellRef, extend?: boolean) => void
  startEdit: (
    cell: GridCellRef,
    how: { seed?: string; caret?: "select" | "end"; expanded?: boolean },
  ) => void
  openRow: (row: number) => void
  sortBy: (column: GridColumn, additive: boolean) => void
  /** These three act from the active cell: on the selection it is part of, or on it alone. */
  setNull: () => void
  setDefault: () => void
  clearCells: () => void
  deleteRows: (rows: readonly number[]) => void
}) {
  return useCallback(
    (event: React.KeyboardEvent) => {
      const state = liveRef.current
      if (state.editing || !fromGrid(event.target) || event.nativeEvent.isComposing) return
      const mod = event.ctrlKey || event.metaKey
      const key = event.key
      const bounds = { rows: state.count, cols: state.ordered.length }
      if (bounds.cols === 0) return
      const here = state.sel.active
      const handled = () => {
        event.preventDefault()
        tabOutRef.current = false
      }

      if (mod && !event.altKey && key.toLowerCase() === "a") {
        handled()
        state.setSelection(selectAll(state.sel, bounds))
        return
      }
      if (mod && !event.altKey && key.toLowerCase() === "z") {
        handled()
        if (event.shiftKey) state.changeSet?.redo()
        else state.changeSet?.undo()
        return
      }
      if (mod && !event.altKey && key.toLowerCase() === "y") {
        handled()
        state.changeSet?.redo()
        return
      }
      if (mod && !event.altKey && key.toLowerCase() === "f" && state.findable) {
        handled()
        findRef.current?.focus()
        findRef.current?.select()
        return
      }

      if (!here) {
        // Nothing is active yet: the first movement key lands on the first cell.
        if (key.startsWith("Arrow") || key === "Home" || key === "End" || key === "Enter") {
          handled()
          moveTo({ row: bounds.rows > 0 ? 0 : HEADER_ROW, col: 0 })
        }
        return
      }
      const onHeader = here.row < 0
      const entry = state.ordered[here.col]

      if (onHeader && event.altKey && (key === "ArrowLeft" || key === "ArrowRight")) {
        handled()
        const width = state.widths[here.col] + (key === "ArrowLeft" ? -16 : 16)
        state.setLayout(setColumnWidth(state.layout, entry.column.key, width))
        return
      }
      if (onHeader && mod && event.shiftKey && (key === "ArrowLeft" || key === "ArrowRight")) {
        handled()
        const target = here.col + (key === "ArrowLeft" ? -1 : 2)
        if (target < 0 || target > bounds.cols) return
        const before = state.ordered[target]?.column.key ?? null
        state.setLayout(moveColumn(state.columns, state.layout, entry.column.key, before))
        moveTo({
          row: HEADER_ROW,
          col: Math.min(Math.max(here.col + (key === "ArrowLeft" ? -1 : 1), 0), bounds.cols - 1),
        })
        return
      }
      if (mod && event.altKey && key.toLowerCase() === "n") {
        handled()
        setNull()
        return
      }
      if (mod && event.altKey && key.toLowerCase() === "d") {
        handled()
        setDefault()
        return
      }

      const page = Math.max(
        Math.floor((state.size.height - HEADER_HEIGHT) / state.rowHeight) - 1,
        1,
      )
      const go = (move: Move) => {
        const next = moveCell(here, move, bounds, page)
        if (!next) return false
        handled()
        moveTo(next, event.shiftKey && move !== "next" && move !== "previous")
        return true
      }

      switch (key) {
        case "ArrowUp":
          go(mod ? "columnStart" : "up")
          return
        case "ArrowDown":
          go(mod ? "columnEnd" : "down")
          return
        case "ArrowLeft":
          go(mod ? "rowStart" : "left")
          return
        case "ArrowRight":
          go(mod ? "rowEnd" : "right")
          return
        case "Home":
          go(mod ? "gridStart" : "rowStart")
          return
        case "End":
          go(mod ? "gridEnd" : "rowEnd")
          return
        case "PageUp":
          go("pageUp")
          return
        case "PageDown":
          go("pageDown")
          return
        case "Tab":
          // Past the last cell — or once Escape has said so — Tab is the
          // browser's again, and the grid is not a trap.
          if (tabOutRef.current) return
          go(event.shiftKey ? "previous" : "next")
          return
        case "Escape":
          if (isRange(state.sel)) {
            handled()
            state.setSelection(collapse(state.sel))
          } else if (state.sel.rows.length > 0) {
            handled()
            state.setSelection({ ...state.sel, rows: [] })
          } else {
            tabOutRef.current = true
          }
          return
        case "Enter":
          handled()
          if (onHeader) sortBy(entry.column, event.shiftKey)
          else if (state.canEdit) startEdit(here, { caret: "select" })
          else openRow(here.row)
          return
        case "F2":
          handled()
          if (!onHeader) startEdit(here, { caret: "end" })
          return
        case " ":
          handled()
          if (onHeader) sortBy(entry.column, event.shiftKey)
          else if (event.shiftKey) {
            // The keyboard's way to the selector column: tick the rows the
            // selection covers, or untick them when they all are.
            const cells = rangeOf(state.sel) ?? { top: here.row, bottom: here.row }
            const span = state.model.ids.slice(cells.top, cells.bottom + 1)
            const all = span.every((id) => state.sel.rows.includes(id))
            state.setSelection({
              ...state.sel,
              rows: toggleRowRange(state.sel.rows, state.model.ids, cells.top, cells.bottom, !all),
            })
          } else if (mod) state.setSelection(selectColumn(state.sel, here.col, bounds))
          else if (state.onOpenRow) openRow(here.row)
          else {
            state.setSelection({
              ...state.sel,
              rows: toggleRow(state.sel.rows, state.model.ids[here.row]),
            })
          }
          return
        case "Delete":
        case "Backspace": {
          if (onHeader || !state.canEdit) return
          handled()
          // Ticked rows are what Delete is about, wherever the cursor is; with
          // none ticked on this page it clears the cells under the selection.
          // A tick can outlive its page — the ids are the owner's — and one
          // that is not here must not turn the key into nothing at all.
          const ticked = tickedRows(state.model, state.sel)
          if (ticked.length > 0) deleteRows(ticked)
          else clearCells()
          return
        }
      }

      // Typing on a cell starts editing it, with what was typed.
      if (!onHeader && key.length === 1 && !mod && !event.altKey && state.canEdit) {
        handled()
        startEdit(here, { seed: key })
      }
    },
    [
      clearCells,
      deleteRows,
      findRef,
      fromGrid,
      liveRef,
      moveTo,
      openRow,
      setDefault,
      setNull,
      sortBy,
      startEdit,
      tabOutRef,
    ],
  )
}
