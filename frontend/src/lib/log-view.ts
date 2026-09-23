"use client"

import { useSyncExternalStore } from "react"

/**
 * How the log pane is drawn, kept in the browser.
 *
 * On the screen and not on the account, for the same reason the theme and the
 * terminal's font are: whether lines wrap is a property of the display you are
 * sitting at. One store rather than per-pane state so every pane on the page
 * agrees, including one that mounts after the setting was changed.
 */
export type LogViewSettings = {
  /** Wrap long lines instead of scrolling sideways. */
  wrap: boolean
  /** Show the timestamp each line was parsed out of. */
  timestamps: boolean
  /**
   * Draw each line by its shapes (`components/logs/log-text.tsx`) and a
   * structured line as its message and fields. Off is the line exactly as it
   * was written, which is one click away for the moment the colouring guessed
   * wrong or the JSON itself is the question.
   */
  highlight: boolean
}

const KEY = "jd.logs.view"

const DEFAULTS: LogViewSettings = { wrap: false, timestamps: true, highlight: true }

let current: LogViewSettings | null = null
const listeners = new Set<() => void>()

function load(): LogViewSettings {
  if (current) return current
  try {
    const raw = window.localStorage.getItem(KEY)
    current = raw ? { ...DEFAULTS, ...(JSON.parse(raw) as Partial<LogViewSettings>) } : DEFAULTS
  } catch {
    current = DEFAULTS
  }
  return current
}

export function setLogView(patch: Partial<LogViewSettings>) {
  current = { ...load(), ...patch }
  try {
    window.localStorage.setItem(KEY, JSON.stringify(current))
  } catch {
    // Private browsing, or storage that is full. The setting still applies for
    // this session, which is better than refusing to change it.
  }
  for (const listener of listeners) listener()
}

export function useLogView(): LogViewSettings {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    () => load(),
    () => DEFAULTS,
  )
}
