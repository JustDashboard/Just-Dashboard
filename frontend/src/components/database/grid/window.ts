/**
 * Which rows and columns are worth drawing.
 *
 * Rows are one fixed height, so the visible slice is two divisions; columns
 * are whatever widths the reader dragged them to, so theirs is a search over
 * running totals. Both return a half-open range `[start, end)` with a few
 * extra on each side: the overscan is what is already there when a fast wheel
 * outruns one frame, and it is the difference between a grid that scrolls and
 * one that flashes blank at its edges.
 */

export interface Slice {
  start: number
  end: number
}

export const EMPTY_SLICE: Slice = { start: 0, end: 0 }

export function sameSlice(a: Slice, b: Slice): boolean {
  return a.start === b.start && a.end === b.end
}

/** The rows a viewport shows. `scrollTop` and `viewport` are the body's, header excluded. */
export function rowSlice(input: {
  scrollTop: number
  viewport: number
  rowHeight: number
  count: number
  overscan: number
}): Slice {
  const { rowHeight, count, overscan } = input
  if (count <= 0 || rowHeight <= 0) return EMPTY_SLICE
  const top = Math.max(input.scrollTop, 0)
  const first = Math.floor(top / rowHeight)
  const last = Math.ceil((top + Math.max(input.viewport, 0)) / rowHeight)
  const start = Math.min(Math.max(first - overscan, 0), count)
  const end = Math.min(Math.max(last + overscan, start), count)
  return { start, end }
}

/** Running totals of a list of widths: `offsets[i]` is where column `i` starts. */
export function offsetsOf(widths: readonly number[]): number[] {
  const offsets = new Array<number>(widths.length + 1)
  offsets[0] = 0
  for (let i = 0; i < widths.length; i++) offsets[i + 1] = offsets[i] + widths[i]
  return offsets
}

/** The last index whose offset is at or before `x`. */
function indexAt(offsets: readonly number[], x: number): number {
  let low = 0
  let high = offsets.length - 2
  while (low < high) {
    const mid = (low + high + 1) >> 1
    if (offsets[mid] <= x) low = mid
    else high = mid - 1
  }
  return Math.max(low, 0)
}

/**
 * The scrolling columns a viewport shows.
 *
 * `offsets` are the running totals of the unpinned columns alone; `scrollLeft`
 * is the scroller's; `viewport` is the width left for them after the gutter
 * and the pinned columns, which stay put and are always drawn.
 */
export function columnSlice(input: {
  scrollLeft: number
  viewport: number
  offsets: readonly number[]
  overscan: number
}): Slice {
  const count = input.offsets.length - 1
  if (count <= 0) return EMPTY_SLICE
  const left = Math.max(input.scrollLeft, 0)
  const right = left + Math.max(input.viewport, 0)
  const first = indexAt(input.offsets, left)
  const last = indexAt(input.offsets, Math.max(right - 1, left))
  const start = Math.max(first - input.overscan, 0)
  const end = Math.min(last + 1 + input.overscan, count)
  return { start, end: Math.max(end, start) }
}

/**
 * The scroll position that brings a span into view, along one axis.
 *
 * `lead` is what covers the start of the viewport without scrolling with it —
 * the sticky header above the rows, the gutter and pinned columns before the
 * rest — so a cell is not "visible" while it sits underneath them. Returns the
 * current position when the span already shows, so a move inside the viewport
 * scrolls nothing.
 */
export function revealOffset(input: {
  /** Where the span starts and ends, in content coordinates past `lead`. */
  start: number
  end: number
  scroll: number
  /** The viewport's size along this axis, `lead` included. */
  viewport: number
  lead: number
}): number {
  const { start, end, scroll, lead } = input
  const room = Math.max(input.viewport - lead, 0)
  if (start < scroll) return Math.max(start, 0)
  if (end > scroll + room) {
    // A span larger than the room shows its start rather than its end.
    return Math.max(Math.min(end - room, start), 0)
  }
  return scroll
}
