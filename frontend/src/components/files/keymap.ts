import { matchName } from "./navigation"
import type { HeldClip } from "./os-clipboard"
import type { FileSearchMode } from "./quick-open"

/**
 * What a key does on the Files page, decided apart from doing it. The page's keydown listener
 * reads the event and the page's state into a press and a state, asks `filesKey`, and carries
 * out the answer. Every rule of the listener is here, in the order it is tried: the first that
 * matches decides, and one that matches without anything to do still stops the rest.
 */

/** The keys that move the active entry through the listing. */
export type MoveKey = "ArrowDown" | "ArrowUp" | "ArrowLeft" | "ArrowRight" | "Home" | "End"

/** Name typing: the letters typed so far, when the last came, and in which folder. */
export type NameTyping = { query: string; time: number; dir: string | null }

/** A gap between letters longer than this starts a new name. */
export const TYPING_PAUSE_MS = 700

/** What the keymap needs of an entry. */
export type KeyEntry = { name: string; path: string; isDir: boolean }

/** A key press: the event's own fields, and where it landed. */
export type FilesKeyPress = {
  key: string
  code: string
  metaKey: boolean
  ctrlKey: boolean
  shiftKey: boolean
  altKey: boolean
  /** `filesOwnKeyboard`: Files has the keyboard, and no dialog, menu or listbox is open. */
  owned: boolean
  /** `isTypingTarget`: a field, a select or an editable region has the key. */
  typing: boolean
  /** `keepsOwnKeys`: a control other than a file name has the key. */
  onControl: boolean
}

export type FilesKeyState<E extends KeyEntry> = {
  /** The folder on screen. */
  path: string | null
  entries: E[]
  active: E | null
  selected: ReadonlySet<string>
  /** Something is on the page's own clipboard. */
  clipHeld: boolean
  canWrite: boolean
  canDestruct: boolean
  canBack: boolean
  canForward: boolean
  /** The folder above, when it is one the server will list. */
  up: string | null
  view: "list" | "grid"
  typed: NameTyping
  /** `performance.now()` at the press, for name typing. */
  now: number
}

export type FilesKeyAction<E extends KeyEntry> =
  | { kind: "search"; mode: FileSearchMode }
  | { kind: "reload" }
  | { kind: "back" }
  | { kind: "forward" }
  | { kind: "up"; path: string }
  | { kind: "new-folder" }
  | { kind: "toggle-hidden" }
  | { kind: "toggle"; entry: E; on: boolean }
  | { kind: "select-all"; paths: string[] }
  | { kind: "hold-clip"; clip: HeldClip }
  | { kind: "hold-paste" }
  /** Name typing starts again; `clear` is what Escape lets go of, if anything. */
  | { kind: "escape"; clear: "selection" | "clip" | null }
  | { kind: "rename"; entry: E }
  | { kind: "delete-selected"; permanent: boolean }
  | { kind: "delete"; entry: E; permanent: boolean }
  | { kind: "open"; entry: E }
  | { kind: "view"; entry: E }
  | { kind: "move"; key: MoveKey; extend: boolean }
  | { kind: "shortcuts" }
  /** Name typing goes on as `typed`; `entry` is the name it reached, if any. */
  | { kind: "find"; typed: NameTyping; entry: E | null }

/** Whether the browser's own handling of the key is stopped, and what the page does. */
export type FilesKeyDecision<E extends KeyEntry> = {
  prevent: boolean
  action: FilesKeyAction<E> | null
}

const NOTHING = { prevent: false, action: null } as const

const take = <E extends KeyEntry>(action: FilesKeyAction<E> | null): FilesKeyDecision<E> => ({
  prevent: true,
  action,
})

/** Up a level, where there is a level up; the key is the listing's either way. */
const goUp = <E extends KeyEntry>(state: FilesKeyState<E>) =>
  take<E>(state.up ? { kind: "up", path: state.up } : null)

export function filesKey<E extends KeyEntry>(
  press: FilesKeyPress,
  state: FilesKeyState<E>,
): FilesKeyDecision<E> {
  if (!press.owned || press.typing) return NOTHING
  const mod = press.metaKey || press.ctrlKey
  const key = press.key.toLowerCase()
  const { active, selected } = state
  if (mod && press.shiftKey && key === "f") return take({ kind: "search", mode: "content" })
  if (mod && !press.shiftKey && (key === "p" || key === "f"))
    return take({ kind: "search", mode: "names" })
  // With Shift or Alt held a refresh stays the browser's own: Shift is its hard refresh.
  if ((press.key === "F5" || (mod && key === "r")) && !press.shiftKey && !press.altKey)
    return take({ kind: "reload" })
  if ((press.altKey && press.key === "ArrowLeft") || (press.metaKey && key === "["))
    return state.canBack ? take({ kind: "back" }) : NOTHING
  if ((press.altKey && press.key === "ArrowRight") || (press.metaKey && key === "]"))
    return state.canForward ? take({ kind: "forward" }) : NOTHING
  if (press.altKey && press.key === "ArrowUp") return goUp(state)
  if (mod && press.shiftKey && key === "n" && state.canWrite) return take({ kind: "new-folder" })
  // Shift+. is ">" on most layouts, so the key's place is read as well as its character.
  if (mod && press.shiftKey && (key === "." || press.code === "Period"))
    return take({ kind: "toggle-hidden" })
  if (mod && press.code === "Space" && active)
    return take({ kind: "toggle", entry: active, on: !selected.has(active.path) })
  if (mod && key === "a")
    return take({ kind: "select-all", paths: state.entries.map((e) => e.path) })
  // Copy, cut and paste are not prevented, so the clipboard events they cause can reach the
  // system clipboard; the page holds what they are for until the event takes it.
  if (mod && (key === "c" || key === "x") && state.canWrite) {
    const paths = selected.size > 0 ? [...selected] : active ? [active.path] : []
    if (paths.length === 0) return NOTHING
    return {
      prevent: false,
      action: { kind: "hold-clip", clip: { mode: key === "c" ? "copy" : "cut", paths } },
    }
  }
  if (mod && key === "v" && state.canWrite && state.path)
    return { prevent: false, action: { kind: "hold-paste" } }
  if (mod || press.altKey) return NOTHING
  switch (press.key) {
    case "Escape":
      return take({
        kind: "escape",
        clear: selected.size > 0 || active ? "selection" : state.clipHeld ? "clip" : null,
      })
    case "F2":
      return active && state.canWrite ? take({ kind: "rename", entry: active }) : NOTHING
    case "Delete":
      if (!state.canDestruct) return NOTHING
      if (selected.size > 0) return take({ kind: "delete-selected", permanent: press.shiftKey })
      return take(active ? { kind: "delete", entry: active, permanent: press.shiftKey } : null)
    case "Backspace":
      return goUp(state)
    case "Enter":
      return active && !press.onControl ? take({ kind: "open", entry: active }) : NOTHING
    case " ":
      return active && !active.isDir && !press.onControl
        ? take({ kind: "view", entry: active })
        : NOTHING
    case "ArrowDown":
    case "ArrowUp":
    case "ArrowLeft":
    case "ArrowRight":
    case "Home":
    case "End":
      if (press.onControl) return NOTHING
      // The details view is a column of rows: across is into a folder and out of this one.
      if (state.view === "list" && press.key === "ArrowRight")
        return take(active?.isDir ? { kind: "open", entry: active } : null)
      if (state.view === "list" && press.key === "ArrowLeft") return goUp(state)
      return take({ kind: "move", key: press.key, extend: press.shiftKey })
    case "?":
      return press.onControl ? NOTHING : take({ kind: "shortcuts" })
    default:
      if (press.onControl || press.key.length !== 1 || press.key === " ") return NOTHING
      return typeToFind(press.key, key, state)
  }
}

/**
 * Typing a name jumps to it. Letters close together build a prefix; the same letter again and
 * again steps through the names that start with it instead, from the one after the active entry.
 */
function typeToFind<E extends KeyEntry>(
  letter: string,
  lower: string,
  state: FilesKeyState<E>,
): FilesKeyDecision<E> {
  const previous = state.typed
  const fresh = previous.dir !== state.path || state.now - previous.time > TYPING_PAUSE_MS
  const repeated = [...previous.query].every((typed) => typed.toLocaleLowerCase() === lower)
  const query = fresh || repeated ? letter : previous.query + letter
  const typed = { query, time: state.now, dir: state.path }
  const cycling = [...query].every((typed) => typed.toLocaleLowerCase() === lower)
  const { active } = state
  const index = active ? state.entries.findIndex((e) => e.path === active.path) : -1
  const next = matchName(
    state.entries.map((e) => e.name),
    cycling ? letter : query,
    cycling ? index : index - 1,
  )
  const entry = next >= 0 ? state.entries[next] : null
  return { prevent: entry !== null, action: { kind: "find", typed, entry } }
}

/**
 * Where an arrow, Home or End takes the active entry, as an index into a listing of `count`
 * drawn `columns` wide: up and down step a whole row of tiles, and with nothing active any arrow
 * starts at the top.
 */
export function moveIndex(key: MoveKey, index: number, count: number, columns: number): number {
  const step = key === "ArrowDown" || key === "ArrowUp" ? columns : 1
  if (key === "Home") return 0
  if (key === "End") return count - 1
  if (index < 0) return 0
  if (key === "ArrowDown" || key === "ArrowRight") return Math.min(count - 1, index + step)
  return Math.max(0, index - step)
}
