"use client"

import { useSyncExternalStore } from "react"

/**
 * The terminal's keyboard shortcuts, and the fact that they are the operator's
 * to change.
 *
 * A web terminal is the one place where a fixed shortcut is guaranteed to be
 * wrong for somebody. The chord has to get past three layers before it means
 * anything: the browser, which owns Ctrl+T and Ctrl+W outright; the page; and
 * the shell, which owns Ctrl+C, Ctrl+D and every Alt chord a reader might use
 * for word motion. There is no default that does not annoy someone, so the
 * defaults here are a starting position rather than a claim.
 *
 * Ctrl+Alt is the default family because it is the one combination neither the
 * browser nor a shell has a use for. Ctrl+Shift is the emulator's own, which is
 * the convention every terminal on Linux already follows.
 *
 * Matching is on `event.code` — the physical key — not `event.key`. A binding
 * recorded on a QWERTY machine has to keep working when the operator switches
 * to a Romanian layout, and `key` is the character the layout produces.
 */
export type ShortcutAction =
  | "session.next"
  | "session.prev"
  | "session.new"
  | `session.${1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9}`
  | "window.next"
  | "window.prev"
  | "window.new"
  | "window.close"
  | `window.${1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9}`
  | "terminal.search"
  | "terminal.copy"
  | "terminal.paste"
  | "terminal.clear"
  | "terminal.fullscreen"
  | "terminal.shortcuts"
  | "workspace.rail"
  | "workspace.tools"

/**
 * Which listener owns an action.
 *
 * Navigation is the page's, because only the page knows what the sessions and
 * windows are. The rest belongs to the pane, because the compose runner reuses
 * the same emulator and needs copy, paste and search without there being a
 * session anywhere. Splitting them this way is what keeps one keydown from
 * being handled twice.
 */
export type ShortcutScope = "navigation" | "terminal"

export type ShortcutSpec = {
  action: ShortcutAction
  scope: ShortcutScope
  group: string
  label: string
  /** Empty means "no default": the operator can bind it, nothing does yet. */
  chord: string
}

export const SHORTCUTS: ShortcutSpec[] = [
  // Sessions.
  {
    action: "session.prev",
    scope: "navigation",
    group: "Sessions",
    label: "Previous session",
    chord: "Ctrl+Alt+ArrowUp",
  },
  {
    action: "session.next",
    scope: "navigation",
    group: "Sessions",
    label: "Next session",
    chord: "Ctrl+Alt+ArrowDown",
  },
  {
    action: "session.new",
    scope: "navigation",
    group: "Sessions",
    label: "New session",
    chord: "Ctrl+Alt+KeyN",
  },
  ...([1, 2, 3, 4, 5, 6, 7, 8, 9] as const).map((n) => ({
    action: `session.${n}` as ShortcutAction,
    scope: "navigation" as const,
    group: "Sessions",
    label: `Session ${n}`,
    chord: `Ctrl+Alt+Digit${n}`,
  })),

  // Windows.
  {
    action: "window.prev",
    scope: "navigation",
    group: "Windows",
    label: "Previous window",
    chord: "Ctrl+Alt+ArrowLeft",
  },
  {
    action: "window.next",
    scope: "navigation",
    group: "Windows",
    label: "Next window",
    chord: "Ctrl+Alt+ArrowRight",
  },
  {
    action: "window.new",
    scope: "navigation",
    group: "Windows",
    label: "New window",
    chord: "Ctrl+Alt+KeyT",
  },
  {
    action: "window.close",
    scope: "navigation",
    group: "Windows",
    label: "Close window",
    chord: "Ctrl+Alt+KeyW",
  },
  ...([1, 2, 3, 4, 5, 6, 7, 8, 9] as const).map((n) => ({
    action: `window.${n}` as ShortcutAction,
    scope: "navigation" as const,
    group: "Windows",
    label: `Window ${n}`,
    chord: `Ctrl+Shift+Digit${n}`,
  })),

  // The workspace panels. Both are one chord that shows and hides, rather than
  // a pair: a panel you cannot see is the only reason to press either, so two
  // separate bindings would mean remembering which of them you needed while
  // looking at the wrong half of the screen.
  {
    action: "workspace.rail",
    scope: "navigation",
    group: "Workspace",
    label: "Sessions panel",
    chord: "Ctrl+Alt+KeyB",
  },
  {
    action: "workspace.tools",
    scope: "navigation",
    group: "Workspace",
    label: "Files & diff panel",
    chord: "Ctrl+Alt+KeyJ",
  },

  // The emulator's own.
  {
    action: "terminal.copy",
    scope: "terminal",
    group: "Terminal",
    label: "Copy the selection",
    chord: "Ctrl+Shift+KeyC",
  },
  {
    action: "terminal.paste",
    scope: "terminal",
    group: "Terminal",
    label: "Paste",
    chord: "Ctrl+Shift+KeyV",
  },
  {
    action: "terminal.search",
    scope: "terminal",
    group: "Terminal",
    label: "Search the scrollback",
    chord: "Ctrl+Shift+KeyF",
  },
  {
    action: "terminal.clear",
    scope: "terminal",
    group: "Terminal",
    label: "Clear the screen",
    chord: "Ctrl+Shift+KeyK",
  },
  {
    action: "terminal.fullscreen",
    scope: "terminal",
    group: "Terminal",
    label: "Fullscreen",
    chord: "Ctrl+Alt+KeyF",
  },
  {
    action: "terminal.shortcuts",
    scope: "terminal",
    group: "Terminal",
    label: "Show the shortcuts",
    chord: "Ctrl+Alt+Slash",
  },
]

const DEFAULTS: Record<string, string> = Object.fromEntries(
  SHORTCUTS.map((s) => [s.action, s.chord]),
)

const KEY = "jd.terminal.keymap"

let overrides: Record<string, string> | null = null
const listeners = new Set<() => void>()
/** The resolved map, rebuilt on write so subscribers get a stable identity. */
let resolved: Record<string, string> = { ...DEFAULTS }

function load(): Record<string, string> {
  if (overrides || typeof window === "undefined") return resolved
  overrides = {}
  try {
    const raw = window.localStorage.getItem(KEY)
    if (raw) overrides = JSON.parse(raw) as Record<string, string>
  } catch {
    // A corrupt store is not worth surfacing: the defaults are a working
    // keyboard, which is what the operator came for.
  }
  resolved = { ...DEFAULTS, ...overrides }
  return resolved
}

function persist() {
  resolved = { ...DEFAULTS, ...(overrides ?? {}) }
  try {
    window.localStorage.setItem(KEY, JSON.stringify(overrides ?? {}))
  } catch {
    // Private browsing, or storage that is full. The binding still applies for
    // this session, which beats refusing to change it.
  }
  for (const listener of listeners) listener()
}

/** Binds an action. An empty chord unbinds it without restoring the default. */
export function bindShortcut(action: ShortcutAction, chord: string) {
  load()
  overrides = { ...(overrides ?? {}), [action]: chord }
  persist()
}

/** Puts one action back to what it shipped with. */
export function resetShortcut(action: ShortcutAction) {
  load()
  const next = { ...(overrides ?? {}) }
  delete next[action]
  overrides = next
  persist()
}

export function resetAllShortcuts() {
  overrides = {}
  persist()
}

export function keymap(): Record<string, string> {
  return load()
}

export function useKeymap(): Record<string, string> {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    () => load(),
    () => resolved,
  )
}

/** True where the operator has changed this binding. */
export function isCustomised(action: ShortcutAction): boolean {
  load()
  return overrides != null && action in overrides
}

/**
 * The chord an event represents, in the stored form.
 *
 * A bare modifier is not a chord: holding Ctrl on the way to Ctrl+Alt+N would
 * otherwise record "Ctrl" and swallow every subsequent key.
 */
export function chordOf(event: KeyboardEvent): string | null {
  const code = event.code
  if (!code || /^(Control|Shift|Alt|Meta)(Left|Right)$/.test(code)) return null
  const parts: string[] = []
  if (event.ctrlKey) parts.push("Ctrl")
  if (event.altKey) parts.push("Alt")
  if (event.shiftKey) parts.push("Shift")
  if (event.metaKey) parts.push("Meta")
  parts.push(code)
  return parts.join("+")
}

/**
 * Whether a chord is safe to hand to the page at all.
 *
 * An unmodified key is what the shell is for: binding `k` to "next window" would
 * make the terminal unusable, and the operator would have no way back because
 * the settings dialog needs typing too. Ctrl alone is nearly as bad — Ctrl+C,
 * Ctrl+D, Ctrl+Z and Ctrl+L all belong to the process in the terminal.
 */
export function chordIsUsable(chord: string): { ok: boolean; why?: string } {
  const parts = chord.split("+")
  const mods = new Set(parts.slice(0, -1))
  if (mods.size === 0) {
    return { ok: false, why: "A plain key goes to the shell. Add Ctrl+Alt or Ctrl+Shift." }
  }
  if (mods.size === 1 && mods.has("Ctrl")) {
    return { ok: false, why: "Ctrl on its own belongs to the shell — Ctrl+C, Ctrl+D, Ctrl+Z." }
  }
  if (mods.size === 1 && mods.has("Shift")) {
    return { ok: false, why: "Shift on its own just types a capital letter." }
  }
  if (mods.size === 1 && mods.has("Alt")) {
    return { ok: false, why: "Alt on its own is Meta, which readline uses for word motion." }
  }
  return { ok: true }
}
const KEY_LABELS: Record<string, string> = {
  ArrowUp: "↑",
  ArrowDown: "↓",
  ArrowLeft: "←",
  ArrowRight: "→",
  Equal: "=",
  Minus: "−",
  Backslash: "\\",
  Slash: "/",
  BracketLeft: "[",
  BracketRight: "]",
  Semicolon: ";",
  Quote: "'",
  Comma: ",",
  Period: ".",
  Backquote: "`",
  Space: "Space",
  Escape: "Esc",
  Enter: "Enter",
  Tab: "Tab",
  Backspace: "Backspace",
  Delete: "Del",
  Home: "Home",
  End: "End",
  PageUp: "PgUp",
  PageDown: "PgDn",
}

/** The chord as somebody would read it aloud. */
export function formatChord(chord: string | undefined): string {
  if (!chord) return "—"
  const parts = chord.split("+")
  const code = parts[parts.length - 1]
  let label = KEY_LABELS[code]
  if (!label) {
    if (code.startsWith("Key")) label = code.slice(3)
    else if (code.startsWith("Digit")) label = code.slice(5)
    else if (code.startsWith("Numpad")) label = `Num ${code.slice(6)}`
    else if (/^F\d+$/.test(code)) label = code
    else label = code
  }
  return [...parts.slice(0, -1), label].join("+")
}

/**
 * Resolves a keydown to an action within one scope.
 *
 * Scoped because two listeners are live at once — the page's and the pane's —
 * and a chord that belongs to the other one must fall through rather than be
 * swallowed.
 */
export function actionFor(
  event: KeyboardEvent,
  scope: ShortcutScope,
  map: Record<string, string>,
): ShortcutAction | null {
  const chord = chordOf(event)
  if (!chord) return null
  for (const spec of SHORTCUTS) {
    if (spec.scope !== scope) continue
    if (map[spec.action] === chord) return spec.action
  }
  return null
}
