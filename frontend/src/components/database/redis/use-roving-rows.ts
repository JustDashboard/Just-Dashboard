"use client"

import { useEffect, useState } from "react"

const ROW = "[data-roving]"

/**
 * One tab stop for a long run of rows, and the arrow keys between them.
 *
 * A list of two thousand keys where every row is a button is two thousand
 * presses of Tab to get past it. Here the run is entered once — on the row
 * that is current, else the first — Up and Down move along it, Home and End
 * go to its ends, and Right and Left open and close a row that folds. Rows
 * mark themselves with `data-roving` and take `tabIndex={-1}`; which one
 * holds the tab stop is kept on the element, since the rows come and go as
 * the list is scrolled and filtered.
 */
export function useRovingRows<T extends HTMLElement>() {
  const [el, attach] = useState<T | null>(null)

  // After every render: rows may have arrived or left with the tab stop.
  useEffect(() => {
    if (!el || el.querySelector(`${ROW}[tabindex="0"]`)) return
    const home =
      el.querySelector<HTMLElement>(`${ROW}[aria-current="true"]`) ??
      el.querySelector<HTMLElement>(ROW)
    home?.setAttribute("tabindex", "0")
  })

  const moveTo = (from: HTMLElement, to: HTMLElement | null | undefined) => {
    if (!to || to === from) return
    from.setAttribute("tabindex", "-1")
    to.setAttribute("tabindex", "0")
    to.focus()
  }

  const onKeyDown = (event: React.KeyboardEvent<T>) => {
    const row = (event.target as HTMLElement).closest<HTMLElement>(ROW)
    if (!row || !el || event.altKey || event.ctrlKey || event.metaKey) return
    const rows = Array.from(el.querySelectorAll<HTMLElement>(ROW))
    const at = rows.indexOf(row)
    const folded = row.getAttribute("aria-expanded")
    switch (event.key) {
      case "ArrowDown":
        moveTo(row, rows[at + 1])
        break
      case "ArrowUp":
        moveTo(row, rows[at - 1])
        break
      case "Home":
        moveTo(row, rows[0])
        break
      case "End":
        moveTo(row, rows[rows.length - 1])
        break
      case "ArrowRight":
        if (folded !== "false") return
        row.click()
        break
      case "ArrowLeft":
        if (folded !== "true") return
        row.click()
        break
      default:
        return
    }
    event.preventDefault()
  }

  /** A row reached with the pointer becomes the tab stop, so Tab comes back to it. */
  const onFocus = (event: React.FocusEvent<T>) => {
    const row = (event.target as HTMLElement).closest<HTMLElement>(ROW)
    if (!row || row.getAttribute("tabindex") === "0") return
    el?.querySelector(`${ROW}[tabindex="0"]`)?.setAttribute("tabindex", "-1")
    row.setAttribute("tabindex", "0")
  }

  return { attach, onKeyDown, onFocus }
}
