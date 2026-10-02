"use client"

import { useCallback, useEffect, useState } from "react"

/** How many rows past each edge are kept drawn, so a fast wheel does not outrun the page. */
const OVERSCAN = 12
/** How many rows the scroller moves before the drawn slice is worked out again. */
const STEP = 4

export type RowSlice = { start: number; end: number }

/** The rows of a fixed-height list that a viewport shows, with the overscan on both sides. */
export function visibleRows(
  scrollTop: number,
  viewport: number,
  rowHeight: number,
  count: number,
): RowSlice {
  if (count <= 0) return { start: 0, end: 0 }
  const first = Math.floor(Math.max(scrollTop, 0) / rowHeight)
  const last = Math.ceil((Math.max(scrollTop, 0) + Math.max(viewport, 0)) / rowHeight)
  const start = Math.min(Math.max(first - OVERSCAN, 0), count)
  return { start, end: Math.min(Math.max(last + OVERSCAN, start), count) }
}

/**
 * Draws only the rows of a long list that are on screen.
 *
 * A database holds as many keys as it likes and a hash as many fields, and
 * every row drawn is a row the browser lays out on each keystroke. Rows here
 * are one height, so which ones show is two divisions; the list keeps its
 * full scroll height through a spacer and the slice is set at its offset.
 */
export function useRowWindow(count: number, rowHeight: number) {
  // The scroller is held as state, not as a ref: what is drawn depends on
  // it, and it arrives after the first render.
  const [el, attach] = useState<HTMLDivElement | null>(null)
  const [box, setBox] = useState({ top: 0, height: 640 })

  // Where the scroller is, to the nearest few rows: the overscan covers the
  // rest, and a list that redrew on every pixel of a wheel would be redrawing
  // the same rows.
  const step = rowHeight * STEP
  const read = useCallback(() => {
    if (!el) return
    const top = Math.floor(el.scrollTop / step) * step
    const height = el.clientHeight
    setBox((held) => (held.top === top && held.height === height ? held : { top, height }))
  }, [el, step])

  useEffect(() => {
    if (!el) return
    // An observer reports once when it starts, which is the first measure.
    const observer = new ResizeObserver(read)
    observer.observe(el)
    return () => observer.disconnect()
  }, [el, read])

  const slice = visibleRows(box.top, box.height + step, rowHeight, count)
  return {
    /** The scroller's `ref`. */
    attach,
    onScroll: read,
    slice,
    /** The list's whole height, which the scroller keeps. */
    height: count * rowHeight,
    /** Where the drawn slice starts. */
    offset: slice.start * rowHeight,
  }
}
