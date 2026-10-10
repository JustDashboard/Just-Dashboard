"use client"

import { useCallback, useState } from "react"

/**
 * How many cards of at least `min` pixels, `gap` apart, an element holds
 * across: the count an `auto-fill` grid would arrive at, as a number the
 * layout can do arithmetic with.
 *
 * The fleet's shelves need it. A shelf of one card that takes a whole row of
 * four leaves three columns of nothing beside it, and a server with a
 * container, a file and a remote database is three such rows; knowing the
 * count, a shelf takes only as many columns as it has cards and the next one
 * stands beside it. CSS can lay out `auto-fill` columns, but it cannot tell a
 * child how many there turned out to be.
 *
 * The element is measured as it is attached, so the first paint already has
 * the right count, and again whenever the shell's width changes under it.
 */
export function useColumns<T extends HTMLElement>(
  min: number,
  gap: number,
): [(element: T | null) => (() => void) | undefined, number] {
  const [columns, setColumns] = useState(1)
  const attach = useCallback(
    (element: T | null) => {
      if (!element) return undefined
      const fit = (width: number) =>
        setColumns(Math.max(1, Math.floor((width + gap) / (min + gap))))
      fit(element.getBoundingClientRect().width)
      const observer = new ResizeObserver(([entry]) => fit(entry.contentRect.width))
      observer.observe(element)
      return () => observer.disconnect()
    },
    [min, gap],
  )
  return [attach, columns]
}
