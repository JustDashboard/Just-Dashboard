"use client"

import { useCallback, useRef } from "react"
import { clampWidth, dropTarget, moveColumn, resolveColumns, setColumnWidth } from "./layout"
import type { AfterRender, GridLiveRef } from "./live"
import { HEADER_ROW, selectCell, toggleAllRows } from "./selection"
import type { GridColumn } from "./types"

/**
 * What the pointer does to the header row.
 *
 * Three gestures share one press and are told apart by where it lands and what
 * it does next: on the strip across a column's right edge it is a resize (and a
 * double-click there fits the column to its content); on a column name it is a
 * sort if the pointer is released where it went down, and a move if it travels.
 * The corner ticks every row. A right-click opens the column's menu.
 *
 * A resize is shown by a draft width while the pointer is down and written to
 * the layout once, on release — the owner persists layouts, and a write per
 * frame of a drag is a write per frame to localStorage.
 */
export function useHeaderGestures({
  liveRef,
  viewportRef,
  afterRef,
  track,
  setDraft,
  setMoving,
  setHeaderMenu,
  fitColumn,
  sortBy,
}: {
  liveRef: GridLiveRef
  viewportRef: { readonly current: HTMLDivElement | null }
  afterRef: { current: AfterRender }
  track: (onMove: (event: PointerEvent) => void, onEnd: () => void) => void
  setDraft: (draft: { key: string; width: number } | null) => void
  setMoving: (moving: { key: string; before: string | null; x: number } | null) => void
  setHeaderMenu: (index: number | null) => void
  fitColumn: (column: GridColumn) => void
  sortBy: (column: GridColumn, additive: boolean) => void
}) {
  // The click that ends a drag is not a request to sort.
  const suppressClick = useRef(false)

  const onPointerDown = useCallback(
    (event: React.PointerEvent) => {
      if (event.button !== 0 || !(event.target instanceof Element)) return
      const state = liveRef.current
      const handle = event.target.closest("[data-resize]")
      if (handle) {
        event.preventDefault()
        const index = Number(handle.getAttribute("data-resize"))
        const key = state.ordered[index].column.key
        const from = event.clientX
        const base = state.widths[index]
        let width = base
        track(
          (move) => {
            width = clampWidth(base + move.clientX - from)
            setDraft({ key, width })
          },
          () => {
            setDraft(null)
            const now = liveRef.current
            if (width !== base) now.setLayout(setColumnWidth(now.layout, key, width))
          },
        )
        return
      }
      if (event.target.closest("[data-column-menu]")) return
      const head = event.target.closest("[data-hcol]")
      if (!head) return
      const index = Number(head.getAttribute("data-hcol"))
      const key = state.ordered[index].column.key
      const from = event.clientX
      let dragged = false
      track(
        (move) => {
          if (!dragged && Math.abs(move.clientX - from) < 5) return
          dragged = true
          const root = viewportRef.current
          if (!root) return
          const now = liveRef.current
          const lead = now.gutter + now.pinnedWidth
          const x = move.clientX - root.getBoundingClientRect().left
          // Over the pinned columns the pointer is in their own coordinates;
          // past them it is in the scrolled ones.
          const content = x < lead ? x - now.gutter : x - now.gutter + root.scrollLeft
          const resolved = resolveColumns(now.columns, now.layout)
          const before = dropTarget(resolved, content)
          const at =
            before === null ? resolved.length : resolved.findIndex((e) => e.column.key === before)
          const edge = now.gutter + now.offsets[at]
          setMoving({
            key,
            before,
            x: at < now.pinnedCount ? edge : Math.max(edge - root.scrollLeft, lead),
          })
        },
        () => {
          if (!dragged) return
          suppressClick.current = true
          setTimeout(() => (suppressClick.current = false))
          const now = liveRef.current
          if (now.moving) now.setLayout(moveColumn(now.columns, now.layout, key, now.moving.before))
          setMoving(null)
        },
      )
    },
    [liveRef, setDraft, setMoving, track, viewportRef],
  )

  const onClick = useCallback(
    (event: React.MouseEvent) => {
      if (!(event.target instanceof Element)) return
      if (suppressClick.current) return
      const state = liveRef.current
      if (event.target.closest("[data-select-all]")) {
        state.setSelection({ ...state.sel, rows: toggleAllRows(state.sel.rows, state.model.ids) })
        return
      }
      if (event.target.closest("[data-resize], [data-column-menu]")) return
      const head = event.target.closest("[data-hcol]")
      if (!head) return
      const index = Number(head.getAttribute("data-hcol"))
      state.setSelection(selectCell(state.sel, { row: HEADER_ROW, col: index }))
      afterRef.current = { reveal: null, focus: true }
      sortBy(state.ordered[index].column, event.shiftKey)
    },
    [afterRef, liveRef, sortBy],
  )

  const onDoubleClick = useCallback(
    (event: React.MouseEvent) => {
      if (!(event.target instanceof Element)) return
      const handle = event.target.closest("[data-resize]")
      if (!handle) return
      fitColumn(liveRef.current.ordered[Number(handle.getAttribute("data-resize"))].column)
    },
    [fitColumn, liveRef],
  )

  const onContextMenu = useCallback(
    (event: React.MouseEvent) => {
      if (!(event.target instanceof Element)) return
      const head = event.target.closest("[data-hcol]")
      if (!head) return
      event.preventDefault()
      setHeaderMenu(Number(head.getAttribute("data-hcol")))
    },
    [setHeaderMenu],
  )

  return { onPointerDown, onClick, onDoubleClick, onContextMenu }
}
