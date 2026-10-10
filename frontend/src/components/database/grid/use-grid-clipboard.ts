"use client"

import { useCallback } from "react"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import type { ChangeAction } from "./change-set"
import {
  clipText,
  decodeGridClip,
  encodeGridClip,
  GRID_MIME,
  parseTSV,
  previewNote,
  toCSV,
  toJSONRows,
  toMarkdown,
  toTSV,
  type GridClip,
} from "./clipboard"
import type { CopyFormat } from "./grid-menus"
import type { GridLiveRef } from "./live"
import { actionBlock, isPreview, valueAt } from "./model"
import { clipCells, planPaste, type PastedCell } from "./paste"
import { isDefault } from "./values"
import type { GridBlockCell, GridCellRef, GridSelectionData } from "./types"

/**
 * What this page last put on the clipboard as plain text, and the cells it was.
 *
 * Two paths carry text alone: the menu's "Copy as", which goes through the
 * asynchronous clipboard, and the menu's "Paste", which reads it back the same
 * way. Text cannot say that a field was NULL rather than empty, or that it was
 * only the first four kilobytes of a value. So when the text that comes back
 * is the text that went out, the cells remembered here are pasted in its place.
 */
let lastCopy: { text: string; clip: GridClip } | null = null

const sameText = (a: string, b: string) => a.replace(/\r\n/g, "\n") === b.replace(/\r\n/g, "\n")

/**
 * Copy, cut and paste.
 *
 * Ctrl+C and Ctrl+V are answered inside the browser's own clipboard events
 * rather than through the asynchronous clipboard API. That API exists only in a
 * secure context, and this dashboard is routinely reached over plain HTTP on a
 * LAN address before its certificate is set up — where a grid that could not
 * copy would be a grid nobody could use. The menu's "Copy as" has no event to
 * ride on, so it does use the API, through `lib/clipboard`, which says so when
 * the browser refuses.
 *
 * What is written is tab-separated text, which every spreadsheet reads, and
 * beside it the same cells in the grid's own format, so a copy pasted back into
 * a grid keeps NULL apart from the empty string — and a cell that was only a
 * preview is refused rather than written somewhere as the whole value.
 */
export function useGridClipboard({
  liveRef,
  fromGrid,
  announce,
  stage,
  clearCells,
}: {
  liveRef: GridLiveRef
  fromGrid: (target: EventTarget | null) => boolean
  announce: (text: string) => void
  /** Stages an action; false when the grid refused it. */
  stage: (action: ChangeAction) => boolean
  clearCells: (at: GridCellRef | null) => void
}) {
  /** The cells a copy takes, as raw values with pending edits applied. */
  const selectionData = useCallback(
    (at: GridCellRef | null): GridSelectionData | null => {
      const state = liveRef.current
      const block = actionBlock(state.model, state.sel, at)
      if (!block) return null
      const previews: GridBlockCell[] = []
      const rows = block.rows.map((row, r) =>
        block.cols.map((entry, c) => {
          if (isPreview(state.model, row, entry.source)) previews.push({ row: r, column: c })
          const value = valueAt(state.model, row, entry)
          return value === undefined || isDefault(value) ? null : value
        }),
      )
      return {
        columns: block.cols.map((entry) => entry.column),
        rowIds: block.rows.map((row) => state.model.ids[row]),
        rows,
        previews,
      }
    },
    [liveRef],
  )

  const copied = useCallback(
    (data: GridSelectionData) => {
      const cells = data.rows.length * data.columns.length
      const said = cells === 1 ? "Copied 1 cell" : `Copied ${cells} cells`
      announce(said + previewNote(data.previews.length))
    },
    [announce],
  )

  const copyAs = useCallback(
    (format: CopyFormat, at: GridCellRef | null = liveRef.current.sel.active) => {
      const data = selectionData(at)
      if (!data) return
      const matrix = data.rows.map((row) => row.map(clipText))
      const names = data.columns.map((column) => column.name)
      const text =
        format === "json"
          ? toJSONRows(data.columns, data.rows)
          : format === "csv"
            ? toCSV(matrix, { header: names })
            : format === "markdown"
              ? toMarkdown(names, matrix)
              : toTSV(format === "tsv-header" ? [names, ...matrix] : matrix)
      void copyText(text).then((ok) => {
        if (!ok) return
        // Only the plain block is something a grid can take back cell for cell.
        lastCopy =
          format === "tsv" ? { text, clip: { cells: data.rows, previews: data.previews } } : null
        copied(data)
      })
    },
    [copied, liveRef, selectionData],
  )

  const pasteCells = useCallback(
    (matrix: readonly (readonly PastedCell[])[]) => {
      const state = liveRef.current
      const changeSet = state.changeSet
      if (!state.canEdit || !changeSet) return
      const plan = planPaste(state.model, state.sel, matrix, {
        canInsert: state.canInsert,
        newRowId: changeSet.newRowId,
      })
      if (!plan) {
        announce("Choose a cell to paste into")
        return
      }
      if (plan.actions.length > 0 && !stage({ type: "batch", actions: plan.actions })) return
      // What was pasted is left selected, so it can be seen and pasted over.
      if (plan.block) {
        state.setSelection({
          ...state.sel,
          anchor: { row: plan.block.top, col: plan.block.left },
          active: { row: plan.block.bottom, col: plan.block.right },
        })
      }
      announce(plan.pasted === 1 ? "Pasted 1 cell" : `Pasted ${plan.pasted} cells`)
      if (plan.skipped > 0) {
        notify.warning(
          plan.skipped === 1 ? "One cell was not pasted" : `${plan.skipped} cells were not pasted`,
          { description: plan.reason },
        )
      }
    },
    [announce, liveRef, stage],
  )

  const pasteText = useCallback(
    (text: string) => {
      if (lastCopy && sameText(lastCopy.text, text)) pasteCells(clipCells(lastCopy.clip))
      else pasteCells(parseTSV(text).map((row) => row.map((field) => ({ text: field }))))
    },
    [pasteCells],
  )

  const onCopy = useCallback(
    (event: React.ClipboardEvent) => {
      const state = liveRef.current
      if (!fromGrid(event.target) || state.editing) return
      const data = selectionData(state.sel.active)
      if (!data) return
      // Written in the event itself rather than through the async clipboard,
      // which a page reached over plain HTTP does not have.
      event.preventDefault()
      const text = toTSV(data.rows.map((row) => row.map(clipText)))
      event.clipboardData.setData("text/plain", text)
      event.clipboardData.setData(GRID_MIME, encodeGridClip(data.rows, data.previews))
      lastCopy = { text, clip: { cells: data.rows, previews: data.previews } }
      copied(data)
    },
    [copied, fromGrid, liveRef, selectionData],
  )

  const onCut = useCallback(
    (event: React.ClipboardEvent) => {
      onCopy(event)
      const state = liveRef.current
      if (event.defaultPrevented && state.canEdit) clearCells(state.sel.active)
    },
    [clearCells, liveRef, onCopy],
  )

  const onPaste = useCallback(
    (event: React.ClipboardEvent) => {
      const state = liveRef.current
      if (!fromGrid(event.target) || state.editing || !state.canEdit) return
      event.preventDefault()
      const exact = decodeGridClip(event.clipboardData.getData(GRID_MIME))
      if (exact) pasteCells(clipCells(exact))
      else pasteText(event.clipboardData.getData("text/plain"))
    },
    [fromGrid, liveRef, pasteCells, pasteText],
  )

  return { copyAs, pasteText, onCopy, onCut, onPaste }
}
