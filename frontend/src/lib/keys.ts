/**
 * The two questions every page-wide key handler asks before it acts.
 *
 * Each area used to write its own: the proxy layout skipped INPUT, TEXTAREA,
 * SELECT and contentEditable and any open dialog or menu; the Sites list
 * treated any dialog in the document as open; Streams ignored dialogs; Files
 * had its own `isTypingTarget`; and none of them knew about Monaco, whose
 * text area is a `div` until the caret is in it. One answer here, read by
 * `useShortcuts` and by the handlers that are not yet on it.
 */

/**
 * Where a key is text: a field, an editable block, a code editor or a
 * terminal. Any `input` counts, ticks included: a box ticked from the keyboard
 * takes Space, and a letter pressed on it never meant the page's shortcut.
 */
export const TYPING_SELECTOR =
  "input, textarea, select, [contenteditable]:not([contenteditable='false']), .monaco-editor, .xterm"

/** Whether a key pressed on this target is somebody typing. */
export function isTypingTarget(target: EventTarget | null): boolean {
  const element = target as Element | null
  return typeof element?.closest === "function" && element.closest(TYPING_SELECTOR) !== null
}

/** What owns the keyboard while it is open, above whatever page is under it. */
export const OWNER_SELECTOR = "[role='dialog'], [role='alertdialog'], [role='menu']"

/**
 * Whether a dialog, an alert dialog or a menu is open, so a key acting on the
 * page would act on one the reader cannot see. Radix leaves a closing one in
 * the document for its exit animation with `data-state="closed"`, and a
 * mounted but hidden one draws no box; neither owns anything.
 */
export function keyOwnedElsewhere(root: ParentNode | null = globalDocument()): boolean {
  if (!root) return false
  return Array.from(root.querySelectorAll<HTMLElement>(OWNER_SELECTOR)).some(
    (node) => node.getAttribute("data-state") !== "closed" && node.getClientRects().length > 0,
  )
}

function globalDocument(): Document | null {
  return typeof document === "undefined" ? null : document
}

/**
 * Shortcuts are written the way the help sheets print them — `n`, `/`,
 * `Shift+R`, `Ctrl/⌘+K`, `g s` for `g` then `s` — and compared in one
 * spelling: modifiers in a fixed order, a letter in lower case, Shift kept
 * only where it is not already in the character (`?` is Shift+/ on most
 * keyboards and not on all). `Ctrl/⌘` is `Mod`, which either key answers.
 */
const MODIFIERS = ["Mod", "Ctrl", "Alt", "Meta", "Shift"] as const
type Modifier = (typeof MODIFIERS)[number]

const ALIASES: Record<string, Modifier> = {
  "ctrl/⌘": "Mod",
  mod: "Mod",
  ctrl: "Ctrl",
  control: "Ctrl",
  alt: "Alt",
  option: "Alt",
  "⌥": "Alt",
  meta: "Meta",
  cmd: "Meta",
  "⌘": "Meta",
  shift: "Shift",
  "⇧": "Shift",
}

function spell(modifiers: Set<Modifier>, key: string) {
  const letter = key.length === 1 && /\p{L}/u.test(key)
  const name = key === " " ? "Space" : letter ? key.toLowerCase() : key
  // A printed character carries its own Shift; a letter and a named key do not.
  const keepShift = name.length > 1 || letter
  return [
    ...MODIFIERS.filter((mod) => modifiers.has(mod) && (mod !== "Shift" || keepShift)),
    name,
  ].join("+")
}

/** One step of a written shortcut (`Shift+R`, `Ctrl/⌘+K`) in the compared spelling. */
export function shortcutName(step: string): string {
  const parts = step.trim().split(/\+(?!$)/)
  const key = parts.pop() ?? ""
  const modifiers = new Set<Modifier>()
  for (const part of parts) {
    const mod = ALIASES[part.toLowerCase()]
    if (mod) modifiers.add(mod)
  }
  return spell(modifiers, key)
}

/** A written shortcut as its steps, each in the compared spelling: `g s` is two. */
export function shortcutSteps(spec: string): string[] {
  return spec.trim().split(/\s+/).map(shortcutName)
}

type KeyEventLike = Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "altKey" | "shiftKey">

/**
 * Whether a key belongs to an input method that is composing text: the Enter
 * that picks a candidate, the Escape that dismisses the list. A field acts on
 * neither, or it applies text the reader has not committed yet and clears
 * what they are spelling. Safari ends the composition before the key that
 * ended it arrives, and marks that key only by its 229 key code.
 */
export function isComposingKey(event: Pick<KeyboardEvent, "isComposing" | "keyCode">): boolean {
  return event.isComposing || event.keyCode === 229
}

/**
 * The names a key press answers to: its exact chord, and the `Mod` spelling
 * when one of Ctrl or ⌘ is held, so `Ctrl/⌘+K` is Ctrl+K on Linux and ⌘K on
 * a Mac without the page asking which it is on.
 */
export function eventNames(event: KeyEventLike): string[] {
  const base = new Set<Modifier>()
  if (event.altKey) base.add("Alt")
  if (event.shiftKey) base.add("Shift")
  const exact = new Set(base)
  if (event.ctrlKey) exact.add("Ctrl")
  if (event.metaKey) exact.add("Meta")
  const names = [spell(exact, event.key)]
  if (event.ctrlKey !== event.metaKey) names.push(spell(new Set([...base, "Mod"]), event.key))
  return names
}

/**
 * Whether a shortcut is one character with no Ctrl, ⌘ or Alt: a letter, a
 * digit, a mark (`/`, `?`) or a capital. These are the keys WCAG 2.1.4 asks
 * a way to turn off, because speech input types them; a chord stays.
 */
export function isCharacterShortcut(spec: string): boolean {
  const first = shortcutSteps(spec)[0] ?? ""
  const parts = first.split(/\+(?!$)/)
  const key = parts.pop() ?? ""
  return [...key].length === 1 && parts.every((part) => part === "Shift")
}
