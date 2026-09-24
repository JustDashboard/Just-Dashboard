"use client"

import { useLayoutEffect, useRef, useState } from "react"

/**
 * The width of the column a list is drawn in — for a row that sets its
 * readings beside its name when there is room and under it when there is not.
 *
 * The window is the wrong thing to ask. A settings page's fields column is
 * what is left of the window once the navigation and, from `xl`, the rail
 * have taken theirs, so at 768 a list that "is on a tablet" is 450 pixels wide
 * and a row that laid its readings out for the window lost its name to them.
 * The choice is still made once and drawn once (§12): a reading that exists
 * in a hidden copy is two answers to every query a test or a screen reader
 * makes. The column is measured before the first paint, so a list never lands
 * in the narrow shape and then flips while it is still rising into place.
 */
export function useColumnWidth<T extends HTMLElement = HTMLDivElement>() {
  const ref = useRef<T>(null)
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const node = ref.current
    if (!node) return
    setWidth(node.getBoundingClientRect().width)
    const observer = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width))
    observer.observe(node)
    return () => observer.disconnect()
  }, [])
  return [ref, width] as const
}
