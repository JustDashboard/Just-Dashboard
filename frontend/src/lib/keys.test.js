import { describe, expect, test } from "bun:test"
import {
  eventNames,
  isCharacterShortcut,
  isTypingTarget,
  keyOwnedElsewhere,
  shortcutName,
  shortcutSteps,
} from "./keys"

// There is no document in a unit test, so an element is a stand-in that
// answers `closest` the way the browser would for the few selector forms the
// guard uses: a tag, a class, an attribute, an attribute with a value, and
// `:not(…)` of one of those.

function simpleMatch(node, selector) {
  const not = selector.match(/^(.*?):not\((.*)\)$/)
  if (not) return simpleMatch(node, not[1]) && !simpleMatch(node, not[2])
  const attr = selector.match(/^\[([\w-]+)(?:='([^']*)')?\]$/)
  if (attr) {
    const value = node.attrs?.[attr[1]]
    return attr[2] === undefined ? value !== undefined : value === attr[2]
  }
  if (selector.startsWith(".")) return (node.classes ?? []).includes(selector.slice(1))
  return node.tag === selector
}

function element(tag, { attrs, classes, parent } = {}) {
  const node = { tag, attrs, classes, parent }
  node.closest = (selectors) => {
    const list = selectors.split(",").map((s) => s.trim())
    for (let at = node; at; at = at.parent) {
      if (list.some((s) => simpleMatch(at, s))) return at
    }
    return null
  }
  return node
}

describe("isTypingTarget", () => {
  test("fields are typing, buttons and the page are not", () => {
    expect(isTypingTarget(element("input"))).toBe(true)
    expect(isTypingTarget(element("textarea"))).toBe(true)
    expect(isTypingTarget(element("select"))).toBe(true)
    expect(isTypingTarget(element("button"))).toBe(false)
    expect(isTypingTarget(element("body"))).toBe(false)
    expect(isTypingTarget(null)).toBe(false)
    expect(isTypingTarget({})).toBe(false)
  })

  test("an editable block is typing, one switched off is not", () => {
    const editable = element("div", { attrs: { contenteditable: "true" } })
    expect(isTypingTarget(element("span", { parent: editable }))).toBe(true)
    expect(isTypingTarget(element("div", { attrs: { contenteditable: "" } }))).toBe(true)
    expect(isTypingTarget(element("div", { attrs: { contenteditable: "false" } }))).toBe(false)
  })

  test("anywhere inside Monaco or a terminal is typing", () => {
    const monaco = element("div", { classes: ["monaco-editor"] })
    const lines = element("div", { classes: ["view-lines"], parent: monaco })
    expect(isTypingTarget(element("span", { parent: lines }))).toBe(true)
    const xterm = element("div", { classes: ["xterm"] })
    expect(isTypingTarget(element("div", { parent: xterm }))).toBe(true)
  })
})

describe("keyOwnedElsewhere", () => {
  const node = (role, { state, drawn = true } = {}) => ({
    role,
    getAttribute: (name) => (name === "data-state" ? (state ?? null) : null),
    getClientRects: () => (drawn ? [{}] : []),
  })
  const root = (nodes) => ({
    querySelectorAll(selector) {
      const roles = [...selector.matchAll(/role='([\w]+)'/g)].map((m) => m[1])
      return nodes.filter((n) => roles.includes(n.role))
    },
  })

  test("an open menu, dialog or alert dialog owns the keyboard", () => {
    expect(keyOwnedElsewhere(root([node("menu", { state: "open" })]))).toBe(true)
    expect(keyOwnedElsewhere(root([node("dialog")]))).toBe(true)
    expect(keyOwnedElsewhere(root([node("alertdialog", { state: "open" })]))).toBe(true)
  })

  test("one closing, one not drawn, and other roles do not", () => {
    expect(keyOwnedElsewhere(root([node("menu", { state: "closed" })]))).toBe(false)
    expect(keyOwnedElsewhere(root([node("dialog", { drawn: false })]))).toBe(false)
    expect(keyOwnedElsewhere(root([node("listbox", { state: "open" })]))).toBe(false)
    expect(keyOwnedElsewhere(root([]))).toBe(false)
    expect(keyOwnedElsewhere(null)).toBe(false)
  })
})

describe("shortcut names", () => {
  const press = (key, mods = {}) => ({
    key,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    ...mods,
  })

  test("a letter, a capital and a mark are spelled as the help writes them", () => {
    expect(eventNames(press("n"))).toEqual([shortcutName("n")])
    expect(eventNames(press("R", { shiftKey: true }))).toEqual([shortcutName("Shift+R")])
    expect(eventNames(press("?", { shiftKey: true }))).toEqual([shortcutName("?")])
    expect(eventNames(press("/"))).toEqual([shortcutName("/")])
  })

  test("Ctrl/⌘ is answered by either key, and not by both", () => {
    expect(eventNames(press("k", { ctrlKey: true }))).toContain(shortcutName("Ctrl/⌘+K"))
    expect(eventNames(press("k", { metaKey: true }))).toContain(shortcutName("Ctrl/⌘+K"))
    expect(eventNames(press("k", { ctrlKey: true }))).toContain(shortcutName("Ctrl+K"))
    expect(eventNames(press("k", { ctrlKey: true, metaKey: true }))).not.toContain(
      shortcutName("Ctrl/⌘+K"),
    )
  })

  test("a sequence is its steps", () => {
    expect(shortcutSteps("g s")).toEqual(["g", "s"])
  })

  test("one character with no Ctrl, ⌘ or Alt is a character shortcut", () => {
    for (const spec of ["n", "/", "?", "Shift+R", "g s", "7"]) {
      expect(isCharacterShortcut(spec)).toBe(true)
    }
    for (const spec of ["Ctrl/⌘+K", "Alt+↑", "Enter", "F5", "Escape"]) {
      expect(isCharacterShortcut(spec)).toBe(false)
    }
  })
})
