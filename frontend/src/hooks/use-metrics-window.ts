"use client"

import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react"
import { useRangePreference } from "@/hooks/use-metrics-history"
import type { MetricsWindow, RangeKey } from "@/lib/metrics-range"

export type WindowControls = {
  window: MetricsWindow
  /** True when the charts are showing a dragged span rather than a named range. */
  zoomed: boolean
  /** How many zoom steps can be undone. */
  depth: number
  setRange: (key: RangeKey) => void
  zoomTo: (from: number, to: number) => void
  /** Steps back to the previous window, ending at the named range. */
  zoomOut: () => void
  /** Abandons the whole zoom stack and returns to the named range. */
  reset: () => void
  /** Slides the current window by a fraction of its own width. */
  pan: (fraction: number) => void
}

/**
 * The window the charts are drawn over, and the history of how it was reached.
 *
 * A stack rather than a single "zoomed" flag, because zooming is exploratory:
 * you narrow to an hour, then to five minutes inside it, and the way back is
 * to the hour rather than all the way out to the day you started from. Losing
 * the intermediate step is what makes zoom-and-reset tedious enough that
 * people stop using it.
 *
 * Deliberately component state rather than a persisted preference: the named
 * range is a standing choice worth remembering across reloads (and is, in
 * localStorage), whereas a zoom is a question being asked right now. Restoring
 * yesterday's zoom on a fresh page load would show a window with no data in it
 * and no obvious way out.
 */
export function useMetricsWindow(): WindowControls {
  const [range, setRange] = useRangePreference()

  /*
   * The zoomed span is mirrored into the address bar, so the window somebody
   * found an incident in is a link they can paste rather than a description
   * ("zoom into 6h, then drag from about 03:10"). A span in the URL on arrival
   * is the bottom of the zoom stack; every change to the stack is written back.
   *
   * The URL is read as an external store rather than in a state initialiser:
   * the server has no address bar, so an initialiser would render an empty
   * stack there and a zoomed one here, and React would report the mismatch.
   * Through `useSyncExternalStore` the server snapshot is "nothing" and the
   * client corrects it after hydration, the way the range preference does.
   */
  const fromUrl = useSyncExternalStore(subscribeNothing, readUrlSpan, readNoSpan)
  const seed = useMemo(() => (fromUrl ? [fromUrl] : []), [fromUrl])
  // Null until the reader touches the stack, so the URL's span stays the
  // bottom of it without an effect copying one into the other.
  const [touched, setStack] = useState<Span[] | null>(null)
  const stack = touched ?? seed

  const top = stack[stack.length - 1]
  const window: MetricsWindow = top ? { key: range, from: top.from, to: top.to } : { key: range }

  useEffect(() => {
    const url = new URL(globalThis.location.href)
    if (top) {
      // Unix seconds, the same shape the API takes, so a link is also a query.
      url.searchParams.set("from", String(Math.floor(top.from / 1000)))
      url.searchParams.set("to", String(Math.floor(top.to / 1000)))
    } else {
      url.searchParams.delete("from")
      url.searchParams.delete("to")
    }
    if (url.href !== globalThis.location.href) {
      globalThis.history.replaceState(globalThis.history.state, "", url)
    }
  }, [top])

  const chooseRange = useCallback(
    (key: RangeKey) => {
      // Picking a range is a statement about what you want to see, so it
      // discards the zoom rather than applying it inside the new range.
      setStack([])
      setRange(key)
    },
    [setRange],
  )

  const zoomTo = useCallback(
    (from: number, to: number) => {
      if (to <= from) return
      setStack((prev) => [...(prev ?? seed), { from, to }])
    },
    [seed],
  )

  const zoomOut = useCallback(() => setStack((prev) => (prev ?? seed).slice(0, -1)), [seed])
  const reset = useCallback(() => setStack([]), [])

  /**
   * Moves the window along the timeline without changing its width.
   *
   * Only meaningful while zoomed — a named range is anchored to now, and
   * panning it would silently turn it into a fixed span that stops following
   * the clock while still being labelled "1h".
   */
  const pan = useCallback(
    (fraction: number) => {
      setStack((prev) => {
        const stack = prev ?? seed
        const current = stack[stack.length - 1]
        if (!current) return stack
        const width = current.to - current.from
        const shift = width * fraction
        const moved = { from: current.from + shift, to: current.to + shift }
        // Never past the present: there is nothing recorded there, and a chart
        // half full of empty future reads as data that stopped arriving.
        const now = Date.now()
        if (moved.to > now) {
          return [...stack.slice(0, -1), { from: now - width, to: now }]
        }
        return [...stack.slice(0, -1), moved]
      })
    },
    [seed],
  )

  return {
    window,
    zoomed: Boolean(top),
    depth: stack.length,
    setRange: chooseRange,
    zoomTo,
    zoomOut,
    reset,
    pan,
  }
}

type Span = { from: number; to: number }

/** The address bar does not change under a mounted page; there is nothing to subscribe to. */
function subscribeNothing() {
  return () => {}
}

function readNoSpan(): Span | null {
  return null
}

// Cached by the search string it was read from: a store's snapshot has to be
// the same object while nothing changed, or React would render forever.
let urlSpanFor = ""
let urlSpan: Span | null = null

function readUrlSpan(): Span | null {
  const search = globalThis.location.search
  if (search === urlSpanFor) return urlSpan
  urlSpanFor = search
  const params = new URLSearchParams(search)
  const from = Number(params.get("from"))
  const to = Number(params.get("to"))
  urlSpan =
    Number.isFinite(from) && Number.isFinite(to) && from > 0 && to > from
      ? { from: from * 1000, to: to * 1000 }
      : null
  return urlSpan
}
