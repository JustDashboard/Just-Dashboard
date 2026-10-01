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
  toCSV,
  toJSONRows,
  toTSV,
} from "./clipboard"
import type { CopyFormat } from "./grid-menus"
import type { GridLiveRef } from "./live"
import { selectedBlock, valueAt } from "./model"
import { planPaste, type PastedCell } from "./paste"
import { isDefault } from "./values"
import type { GridSelectionData } from "./types"

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
 * a grid keeps NULL apart from the empty string.
 */
export function useGridClipboard({
  liveRef,
  fromGrid,
  announce,
  stage,
  clearSelection,
}: {
  liveRef: GridLiveRef
  fromGrid: (target: EventTarget | null) => boolean
  announce: (text: string) => void
  stage: (action: ChangeAction) => void
  clearSelection: () => void
}) {
  /** The selection as raw values, with pending edits applied. */
  const selectionData = useCallback((): GridSelectionData | null => {
    const state = liveRef.current
    const block = selectedBlock(state.model, state.sel)
    if (!block) return null
    return {
      columns: block.cols.map((entry) => entry.column),
      rowIds: block.rows.map((row) => state.model.ids[row]),
      rows: block.rows.map((row) =>
        block.cols.map((entry) => {
          const value = valueAt(state.model, row, entry)
          return value === undefined || isDefault(value) ? null : value
        }),
      ),
    }
  }, [liveRef])

  const copied = useCallback(
    (data: GridSelectionData) => {
      const cells = data.rows.length * data.columns.length
      announce(cells === 1 ? "Copied 1 cell" : `Copied ${cells} cells`)
    },
    [announce],
  )

  const copyAs = useCallback(
    (format: CopyFormat) => {
      const data = selectionData()
      if (!data) return
      const matrix = data.rows.map((row) => row.map(clipText))
      const names = data.columns.map((column) => column.name)
      const text =
        format === "json"
          ? toJSONRows(data.columns, data.rows)
          : format === "csv"
            ? toCSV(matrix, { header: names })
            : toTSV(format === "tsv-header" ? [names, ...matrix] : matrix)
      void copyText(text).then((ok) => ok && copied(data))
    },
    [copied, selectionData],
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
      if (plan.actions.length > 0) stage({ type: "batch", actions: plan.actions })
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
    (text: string) =>
      pasteCells(parseTSV(text).map((row) => row.map((field) => ({ text: field })))),
    [pasteCells],
  )

  const onCopy = useCallback(
    (event: React.ClipboardEvent) => {
      if (!fromGrid(event.target) || liveRef.current.editing) return
      const data = selectionData()
      if (!data) return
      // Written in the event itself rather than through the async clipboard,
      // which a page reached over plain HTTP does not have.
      event.preventDefault()
      event.clipboardData.setData("text/plain", toTSV(data.rows.map((row) => row.map(clipText))))
      event.clipboardData.setData(GRID_MIME, encodeGridClip(data.rows))
      copied(data)
    },
    [copied, fromGrid, liveRef, selectionData],
  )

  const onCut = useCallback(
    (event: React.ClipboardEvent) => {
      onCopy(event)
      if (event.defaultPrevented && liveRef.current.canEdit) clearSelection()
    },
    [clearSelection, liveRef, onCopy],
  )

  const onPaste = useCallback(
    (event: React.ClipboardEvent) => {
      const state = liveRef.current
      if (!fromGrid(event.target) || state.editing || !state.canEdit) return
      event.preventDefault()
      const exact = decodeGridClip(event.clipboardData.getData(GRID_MIME))
      if (exact) pasteCells(exact.map((row) => row.map((value) => ({ value }))))
      else pasteText(event.clipboardData.getData("text/plain"))
    },
    [fromGrid, liveRef, pasteCells, pasteText],
  )

  return { copyAs, pasteText, onCopy, onCut, onPaste }
}
