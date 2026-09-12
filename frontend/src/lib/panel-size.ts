"use client"

import { useCallback, useSyncExternalStore } from "react"

/**
 * How wide the operator dragged a side panel, kept in the browser.
 *
 * On the screen rather than on the account, for the same reason the theme and
 * the terminal's font are: how much of a 13" laptop the file tree deserves is
 * a property of the laptop. It also means a width survives a reload and a
 * navigation, which is the whole reason to drag one — a panel that snapped
 * back to 21rem on every visit would be adjusted once and then left alone.
 *
 * Stored as a number of pixels. Clamping is the caller's, because only the
 * page knows what else is on the row: a stored width that was reasonable on a
 * wide monitor has to give way on a narrow one rather than push the terminal
 * off the screen.
 */
const KEY = "jd.panel.sizes"

let sizes: Record<string, number> | null = null
const listeners = new Set<() => void>()

function load(): Record<string, number> {
  if (sizes) return sizes
  sizes = {}
  if (typeof window === "undefined") return sizes
  try {
    const raw = window.localStorage.getItem(KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as Record<string, unknown>
      for (const [k, v] of Object.entries(parsed)) {
        if (typeof v === "number" && Number.isFinite(v) && v > 0) sizes[k] = v
      }
    }
  } catch {
    // A corrupt store just means the defaults, which are a working layout.
  }
  return sizes
}

function persist() {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(sizes ?? {}))
  } catch {
    // Private browsing or a full quota. The width still applies for this
    // session, which beats refusing to move the panel.
  }
}

/**
 * Sets a panel's width.
 *
 * `commit` is what reaches localStorage: a drag calls this at pointer rate and
 * writing the store on every frame is a synchronous serialise per mousemove,
 * so the write waits for the pointer to come up.
 */
export function setPanelSize(key: string, px: number, commit = true) {
  load()
  sizes = { ...sizes, [key]: Math.round(px) }
  if (commit) persist()
  for (const listener of listeners) listener()
}

/** Puts a panel back to whatever its page considers normal. */
export function resetPanelSize(key: string) {
  load()
  const next = { ...sizes }
  delete next[key]
  sizes = next
  persist()
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/**
 * The stored width for a panel, or its fallback.
 *
 * The server has no localStorage, so the first render is always the fallback
 * and the stored value arrives on hydration — the same bargain the terminal's
 * font settings make, and invisible here because the panel is inside a
 * client-rendered page.
 */
export function usePanelSize(
  key: string,
  fallback: number,
): [number, (px: number, commit?: boolean) => void, () => void] {
  const width = useSyncExternalStore(
    subscribe,
    () => load()[key] ?? fallback,
    () => fallback,
  )
  const set = useCallback((px: number, commit = true) => setPanelSize(key, px, commit), [key])
  const reset = useCallback(() => resetPanelSize(key), [key])
  return [width, set, reset]
}
