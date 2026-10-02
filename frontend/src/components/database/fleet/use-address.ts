"use client"

import { useCallback, useState } from "react"
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

/**
 * A field whose text is in the address: what is typed is held here and shown
 * at once, and the address follows it.
 *
 * Bound straight to the address, a field is re-given its own value a moment
 * after every keystroke — the address is read back asynchronously — and the
 * caret jumps to the end of the text each time, so a letter typed in the
 * middle of a word lands there and the next one does not. The field therefore
 * owns its text. What it wrote to the address is remembered until it is read
 * back, which is how a change that came from somewhere else — Back, a link —
 * is told from its own echo and taken as the new text.
 */
export function useTypedParam(key: string): [string, (text: string) => void] {
  const { read, set } = useAddress()
  const said = read(key)
  const [box, setBox] = useState({ text: said, seen: said, sent: [] as string[] })
  if (said !== box.seen) {
    const echo = box.sent.indexOf(said)
    setBox(
      echo >= 0
        ? { ...box, seen: said, sent: box.sent.slice(echo + 1) }
        : { text: said, seen: said, sent: [] },
    )
  }
  const type = (text: string) => {
    setBox((held) => ({ ...held, text, sent: [...held.sent, text] }))
    set({ [key]: text }, "replace")
  }
  return [box.text, type]
}
