"use client"

import { useCallback, useState, useSyncExternalStore } from "react"

/**
 * How a page was arranged, kept in the browser.
 *
 * Every page in this app is unmounted the moment you navigate away from it, so
 * anything held in `useState` is gone by the time you come back: the file
 * panel you closed on the terminal page is open again, the folder you
 * collapsed is expanded, the tab you were on is the default one. That is
 * indistinguishable from the dashboard ignoring you, and it is worst on
 * exactly the screens somebody leaves and returns to all day.
 *
 * The line this store draws is between **how the page is arranged** and **what
 * you were looking at**. A hidden panel, a collapsed group, a chosen tab, a
 * sort order, a "show system accounts" switch are all decisions about the
 * furniture, and they are remembered. A search box, a selected row, an open
 * dialog and a half-filled form are the question being asked right now, and
 * they are not — a page that restored yesterday's filter would show an empty
 * table with no obvious reason for it, which is the failure this store exists
 * to avoid rather than one to introduce from the other side. The terminal's
 * open session and window are the one selection kept: there the selection is
 * the tab you had open, and a tab bar that forgot it would be broken.
 *
 * On the screen and not on the account, for the same reason the theme and the
 * terminal's font are: whether the file tree is worth a fifth of the window is
 * a property of the window. localStorage rather than session, so a reload
 * keeps it too — the state survives an upgrade restarting the backend under
 * the tab, which is a thing that happens here.
 *
 * Sibling of `panel-size.ts`, which does the same for a dragged width and
 * stays separate: a width is a number with its own clamping rules and its own
 * "reset to normal", and folding it in here would make both stores worse.
 */
const KEY = "jd.view.state"

let state: Record<string, unknown> | null = null

const listeners = new Set<() => void>()

function load(): Record<string, unknown> {
  if (state) return state
  state = {}
  if (typeof window === "undefined") return state
  try {
    const raw = window.localStorage.getItem(KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as unknown
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
        state = { ...(parsed as Record<string, unknown>) }
      }
    }
  } catch {
    // A corrupt store just means the defaults, which are a working layout.
  }
  return state
}

function persist() {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(state ?? {}))
  } catch {
    // Private browsing or a full quota. The choice still applies for this
    // session, which beats refusing to close the panel.
  }
}

/**
 * Whether a stored value is still the shape its page expects.
 *
 * Keys outlive the code that wrote them — a toggle becomes a three-way switch,
 * a tab is renamed — and a value of the wrong shape would be handed to a
 * component as if it were fine. The test is deliberately shallow: it catches
 * the kind that changed, which is what a rewrite actually does, and does not
 * try to validate the inside of an object nobody has described to it.
 */
function usable(value: unknown, fallback: unknown): boolean {
  if (typeof value !== typeof fallback) return false
  if (typeof value !== "object" || value === null || fallback === null) return true
  return Array.isArray(value) === Array.isArray(fallback)
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/**
 * `useState`, for a decision about the page's furniture.
 *
 * Same shape as `useState` — including the updater form — so adopting it on a
 * page is a one-line change and reading it later needs no explanation. `key`
 * is a dotted path naming the page and the thing (`terminal.tools`,
 * `files.sort`); it is stored, so renaming one silently resets it, which is
 * the correct outcome for a control that has changed enough to be renamed.
 *
 * The server has no localStorage, so the first paint is always the fallback
 * and the stored value arrives on hydration. `useSyncExternalStore` is what
 * makes that legal rather than a mismatch: React renders the server snapshot,
 * then re-renders once against the real one.
 */
export function useViewState<T>(
  key: string,
  fallback: T,
): [T, (next: T | ((prev: T) => T)) => void] {
  // The fallback as it was on the first render, kept because
  // `useSyncExternalStore` compares snapshots by identity: handing back a
  // caller's inline `{ key: "name", dir: "asc" }` would be a new object every
  // render and would spin. State rather than a ref so the value is one React
  // already owns, and it is written once and never again.
  const [initial] = useState(() => fallback)

  const read = useCallback((): T => {
    const stored = load()[key]
    return (stored !== undefined && usable(stored, initial) ? stored : initial) as T
  }, [key, initial])

  const value = useSyncExternalStore(subscribe, read, () => initial)

  const set = useCallback(
    (next: T | ((prev: T) => T)) => {
      const stored = load()[key]
      const previous = (stored !== undefined && usable(stored, initial) ? stored : initial) as T
      const resolved = typeof next === "function" ? (next as (prev: T) => T)(previous) : next
      state = { ...load(), [key]: resolved }
      persist()
      for (const listener of listeners) listener()
    },
    [key, initial],
  )

  return [value, set]
}
