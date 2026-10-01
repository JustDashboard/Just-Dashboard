"use client"

import { useCallback } from "react"
import { useSearchParams } from "next/navigation"

/**
 * The part of a section-wide page's state that a pasted link should carry:
 * which reading narrows the fleet, which engines are shown, what was searched
 * for, which way of adding a database is open.
 *
 * `set` writes several keys as one change. A choice somebody made — a tile, a
 * chip, a way in — is a history entry, so Back undoes it; text being typed
 * replaces the entry it is typed into rather than leaving one per keystroke.
 * `null` or `""` clears a key.
 */
export function useAddress() {
  const search = useSearchParams()
  const set = useCallback(
    (changes: Record<string, string | null>, how: "push" | "replace" = "push") => {
      const url = new URL(window.location.href)
      for (const [key, value] of Object.entries(changes)) {
        if (value) url.searchParams.set(key, value)
        else url.searchParams.delete(key)
      }
      if (url.href === window.location.href) return
      const next = `${url.pathname}${url.search}${url.hash}`
      if (how === "push") window.history.pushState(null, "", next)
      else window.history.replaceState(null, "", next)
    },
    [],
  )
  const read = useCallback((key: string) => search.get(key) ?? "", [search])
  return { read, set }
}
