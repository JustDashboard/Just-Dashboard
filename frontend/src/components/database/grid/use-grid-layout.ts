"use client"

import { useCallback } from "react"
import { useViewState } from "@/lib/view-state"
import { EMPTY_LAYOUT, isLayout } from "./layout"
import type { GridLayout } from "./types"

/**
 * A grid's column arrangement, remembered on this screen.
 *
 * The grid stores nothing itself: it is handed a layout and reports changes to
 * it. This is the two lines an owner would otherwise write to keep that in
 * `useViewState` — widths, order, hidden and pinned columns are furniture, so
 * they belong in localStorage with every other decision about how a page is
 * arranged. `key` names the table (`databases.3.data.layout.public.orders`);
 * a different key is a different, independent arrangement.
 *
 * What comes back out of storage is checked before it is used: a key written by
 * an older build, or edited by hand, falls back to the default arrangement
 * instead of reaching the grid as a layout with no `widths`.
 */
export function useGridLayout(
  key: string,
): [GridLayout, (next: GridLayout | ((previous: GridLayout) => GridLayout)) => void, () => void] {
  const [stored, setStored] = useViewState<GridLayout>(key, EMPTY_LAYOUT)
  const layout = isLayout(stored) ? stored : EMPTY_LAYOUT
  const setLayout = useCallback(
    (next: GridLayout | ((previous: GridLayout) => GridLayout)) =>
      setStored((previous) => {
        const base = isLayout(previous) ? previous : EMPTY_LAYOUT
        return typeof next === "function" ? next(base) : next
      }),
    [setStored],
  )
  const reset = useCallback(() => setStored(EMPTY_LAYOUT), [setStored])
  return [layout, setLayout, reset]
}
