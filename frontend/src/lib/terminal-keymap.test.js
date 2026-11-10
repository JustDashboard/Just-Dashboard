import { afterEach, beforeEach, expect, test } from "bun:test"
import { SHORTCUTS, actionFor, chordIsUsable, chordOf, formatChord } from "./terminal-keymap"

/** A keydown, as much of one as the keymap reads. */
const key = (code, mods = "") => ({
  code,
  ctrlKey: mods.includes("c"),
  altKey: mods.includes("a"),
  shiftKey: mods.includes("s"),
  metaKey: mods.includes("m"),
})

const DEFAULTS = Object.fromEntries(SHORTCUTS.map((s) => [s.action, s.chord]))

// ------------------------------------------------------------- the defaults

test("every action has one binding, and no two actions share a chord", () => {
  const actions = SHORTCUTS.map((s) => s.action)
  expect(new Set(actions).size).toBe(actions.length)
  const chords = SHORTCUTS.map((s) => s.chord).filter(Boolean)
  expect(new Set(chords).size).toBe(chords.length)
})

test("every default is a chord the page may take from the shell", () => {
  for (const spec of SHORTCUTS) {
    expect([spec.action, chordIsUsable(spec.chord).ok]).toEqual([spec.action, true])
  }
})

test("the defaults are the Ctrl+Alt family, with the emulator's own on Ctrl+Shift", () => {
  expect(DEFAULTS["session.new"]).toBe("Ctrl+Alt+KeyN")
  expect(DEFAULTS["session.prev"]).toBe("Ctrl+Alt+ArrowUp")
  expect(DEFAULTS["session.next"]).toBe("Ctrl+Alt+ArrowDown")
  expect(DEFAULTS["session.3"]).toBe("Ctrl+Alt+Digit3")
  expect(DEFAULTS["window.new"]).toBe("Ctrl+Alt+KeyT")
  expect(DEFAULTS["window.close"]).toBe("Ctrl+Alt+KeyW")
  expect(DEFAULTS["window.prev"]).toBe("Ctrl+Alt+ArrowLeft")
  expect(DEFAULTS["window.next"]).toBe("Ctrl+Alt+ArrowRight")
  expect(DEFAULTS["window.7"]).toBe("Ctrl+Shift+Digit7")
  expect(DEFAULTS["pane.split.left"]).toBe("Ctrl+Alt+Shift+ArrowLeft")
  expect(DEFAULTS["pane.split.down"]).toBe("Ctrl+Alt+Shift+ArrowDown")
  expect(
    ["left", "right", "up", "down"].map((direction) => DEFAULTS[`pane.focus.${direction}`]),
  ).toEqual(["Ctrl+Alt+KeyH", "Ctrl+Alt+KeyL", "Ctrl+Alt+KeyI", "Ctrl+Alt+KeyK"])
  expect(DEFAULTS["pane.next"]).toBe("Ctrl+Alt+KeyP")
  expect(DEFAULTS["workspace.rail"]).toBe("Ctrl+Alt+KeyB")
  expect(DEFAULTS["workspace.tools"]).toBe("Ctrl+Alt+KeyJ")
  expect(DEFAULTS["terminal.copy"]).toBe("Ctrl+Shift+KeyC")
  expect(DEFAULTS["terminal.paste"]).toBe("Ctrl+Shift+KeyV")
  expect(DEFAULTS["terminal.search"]).toBe("Ctrl+Shift+KeyF")
  expect(DEFAULTS["terminal.clear"]).toBe("Ctrl+Shift+KeyK")
  expect(DEFAULTS["terminal.fullscreen"]).toBe("Ctrl+Alt+KeyF")
  expect(DEFAULTS["terminal.shortcuts"]).toBe("Ctrl+Alt+Slash")
})

test("the nine sessions, the nine windows and the four directions are all there", () => {
  const has = (action) => SHORTCUTS.some((s) => s.action === action)
  for (let n = 1; n <= 9; n++)
    expect([n, has(`session.${n}`), has(`window.${n}`)]).toEqual([n, true, true])
  for (const direction of ["left", "right", "up", "down"]) {
    expect([direction, has(`pane.split.${direction}`), has(`pane.focus.${direction}`)]).toEqual([
      direction,
      true,
      true,
    ])
  }
})

test("copy, paste, search and clear are the pane's; everything that moves between things is the page's", () => {
  const scope = Object.fromEntries(SHORTCUTS.map((s) => [s.action, s.scope]))
  for (const action of Object.keys(scope)) {
    expect([action, scope[action]]).toEqual([
      action,
      action.startsWith("terminal.") ? "terminal" : "navigation",
    ])
  }
})

// ------------------------------------------------------------------ a chord

test("a chord is its modifiers in one order and the physical key", () => {
  expect(chordOf(key("KeyN", "ca"))).toBe("Ctrl+Alt+KeyN")
  expect(chordOf(key("ArrowLeft", "sac"))).toBe("Ctrl+Alt+Shift+ArrowLeft")
  expect(chordOf(key("KeyK", "mcsa"))).toBe("Ctrl+Alt+Shift+Meta+KeyK")
  expect(chordOf(key("KeyK"))).toBe("KeyK")
})

test("a modifier held on the way to a chord is not a chord", () => {
  for (const code of ["ControlLeft", "ControlRight", "ShiftLeft", "AltRight", "MetaLeft"]) {
    expect(chordOf(key(code, "c"))).toBeNull()
  }
  expect(chordOf(key("", "ca"))).toBeNull()
  expect(chordOf(key(undefined, "ca"))).toBeNull()
})

test("a chord the shell needs is refused, and says whose it is", () => {
  expect(chordIsUsable("KeyK")).toEqual({
    ok: false,
    why: "A plain key goes to the shell. Add Ctrl+Alt or Ctrl+Shift.",
  })
  expect(chordIsUsable("Ctrl+KeyC").ok).toBe(false)
  expect(chordIsUsable("Ctrl+KeyC").why).toContain("belongs to the shell")
  expect(chordIsUsable("Shift+KeyA").why).toContain("capital letter")
  expect(chordIsUsable("Alt+KeyB").why).toContain("word motion")
  // Meta alone is left to the operator: no shell reads it.
  expect(chordIsUsable("Meta+KeyK")).toEqual({ ok: true })
  expect(chordIsUsable("Ctrl+Alt+KeyK")).toEqual({ ok: true })
  expect(chordIsUsable("Ctrl+Shift+KeyK")).toEqual({ ok: true })
  expect(chordIsUsable("Alt+Shift+KeyK")).toEqual({ ok: true })
})

test("a chord is read aloud by its key's face, not its code", () => {
  expect(formatChord("Ctrl+Alt+KeyN")).toBe("Ctrl+Alt+N")
  expect(formatChord("Ctrl+Shift+Digit7")).toBe("Ctrl+Shift+7")
  expect(formatChord("Ctrl+Alt+ArrowUp")).toBe("Ctrl+Alt+↑")
  expect(formatChord("Ctrl+Alt+Shift+ArrowLeft")).toBe("Ctrl+Alt+Shift+←")
  expect(formatChord("Ctrl+Alt+Slash")).toBe("Ctrl+Alt+/")
  expect(formatChord("Ctrl+Alt+Minus")).toBe("Ctrl+Alt+−")
  expect(formatChord("Ctrl+Alt+Numpad4")).toBe("Ctrl+Alt+Num 4")
  expect(formatChord("Ctrl+Alt+F11")).toBe("Ctrl+Alt+F11")
  expect(formatChord("Ctrl+Alt+PageDown")).toBe("Ctrl+Alt+PgDn")
  expect(formatChord("Ctrl+Alt+Escape")).toBe("Ctrl+Alt+Esc")
  expect(formatChord("Ctrl+Alt+IntlBackslash")).toBe("Ctrl+Alt+IntlBackslash")
  expect(formatChord("")).toBe("—")
  expect(formatChord(undefined)).toBe("—")
})

// --------------------------------------------------------------- resolving

test("each default chord resolves to its action in the scope that owns it, and to nothing in the other", () => {
  for (const spec of SHORTCUTS) {
    const parts = spec.chord.split("+")
    const event = key(
      parts.at(-1),
      parts
        .slice(0, -1)
        .map((mod) => mod[0].toLowerCase())
        .join(""),
    )
    const other = spec.scope === "terminal" ? "navigation" : "terminal"
    expect([spec.action, actionFor(event, spec.scope, DEFAULTS)]).toEqual([
      spec.action,
      spec.action,
    ])
    expect([spec.action, actionFor(event, other, DEFAULTS)]).toEqual([spec.action, null])
  }
})

test("the physical key decides, so a binding survives another layout", () => {
  // On a French keyboard the key in Q's place types "a"; its code is still KeyQ.
  const map = { ...DEFAULTS, "window.close": "Ctrl+Alt+KeyQ" }
  expect(actionFor({ ...key("KeyQ", "ca"), key: "a" }, "navigation", map)).toBe("window.close")
  expect(actionFor({ ...key("KeyA", "ca"), key: "q" }, "navigation", map)).toBeNull()
})

test("a key that is bound to nothing, or only half a chord, falls through", () => {
  expect(actionFor(key("KeyZ", "ca"), "navigation", DEFAULTS)).toBeNull()
  expect(actionFor(key("KeyN", "c"), "navigation", DEFAULTS)).toBeNull()
  expect(actionFor(key("ControlLeft", "c"), "navigation", DEFAULTS)).toBeNull()
  // An action unbound by its operator answers to no key at all.
  expect(actionFor(key("KeyN", "ca"), "navigation", { ...DEFAULTS, "session.new": "" })).toBeNull()
})

// ------------------------------------------------------- the operator's own

let copy = 0
let storage
const KEY = "jd.terminal.keymap"
const load = () => import(`./terminal-keymap?copy=${copy++}`)

beforeEach(() => {
  const map = new Map()
  storage = {
    getItem: (name) => (map.has(name) ? map.get(name) : null),
    setItem: (name, value) => map.set(name, String(value)),
    map,
  }
  globalThis.window = { localStorage: storage }
})

afterEach(() => {
  delete globalThis.window
})

test("a binding the operator changed is kept, applies at once and is told to whoever is listening", async () => {
  const keys = await load()
  expect(keys.isCustomised("window.close")).toBe(false)
  const before = keys.keymap()
  keys.bindShortcut("window.close", "Ctrl+Alt+KeyQ")
  expect(keys.keymap()["window.close"]).toBe("Ctrl+Alt+KeyQ")
  expect(keys.keymap()["window.new"]).toBe("Ctrl+Alt+KeyT")
  expect(keys.isCustomised("window.close")).toBe(true)
  // A new object each time, so a reader comparing by identity sees the change.
  expect(keys.keymap()).not.toBe(before)
  expect(JSON.parse(storage.map.get(KEY))).toEqual({ "window.close": "Ctrl+Alt+KeyQ" })
  expect(actionFor(key("KeyQ", "ca"), "navigation", keys.keymap())).toBe("window.close")
  expect(actionFor(key("KeyW", "ca"), "navigation", keys.keymap())).toBeNull()
})

test("an empty chord unbinds an action without bringing its default back", async () => {
  const keys = await load()
  keys.bindShortcut("terminal.clear", "")
  expect(keys.keymap()["terminal.clear"]).toBe("")
  expect(keys.isCustomised("terminal.clear")).toBe(true)
  expect(actionFor(key("KeyK", "cs"), "terminal", keys.keymap())).toBeNull()
})

test("one action can be put back, or all of them", async () => {
  const keys = await load()
  keys.bindShortcut("window.close", "Ctrl+Alt+KeyQ")
  keys.bindShortcut("terminal.clear", "")
  keys.resetShortcut("window.close")
  expect(keys.keymap()["window.close"]).toBe("Ctrl+Alt+KeyW")
  expect(keys.keymap()["terminal.clear"]).toBe("")
  expect(JSON.parse(storage.map.get(KEY))).toEqual({ "terminal.clear": "" })
  keys.resetAllShortcuts()
  expect(keys.keymap()).toEqual(DEFAULTS)
  expect(JSON.parse(storage.map.get(KEY))).toEqual({})
  expect(keys.isCustomised("terminal.clear")).toBe(false)
})

test("the bindings kept from an earlier visit are read back over the defaults", async () => {
  storage.setItem(KEY, JSON.stringify({ "session.new": "Ctrl+Alt+KeyM" }))
  const keys = await load()
  expect(keys.keymap()["session.new"]).toBe("Ctrl+Alt+KeyM")
  expect(keys.keymap()["session.next"]).toBe("Ctrl+Alt+ArrowDown")
  expect(keys.isCustomised("session.new")).toBe(true)
})

test("a store that cannot be read or written leaves a working keyboard", async () => {
  storage.setItem(KEY, "{not json")
  const corrupt = await load()
  expect(corrupt.keymap()).toEqual(DEFAULTS)

  globalThis.window = {
    get localStorage() {
      throw new Error("storage is disabled")
    },
  }
  const refused = await load()
  expect(refused.keymap()).toEqual(DEFAULTS)
  // The change still holds for this visit, which beats refusing to make it.
  refused.bindShortcut("window.close", "Ctrl+Alt+KeyQ")
  expect(refused.keymap()["window.close"]).toBe("Ctrl+Alt+KeyQ")
})

test("on the server the keymap is the defaults", async () => {
  delete globalThis.window
  const keys = await load()
  expect(keys.keymap()).toEqual(DEFAULTS)
})
