import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Keycaps, ShortcutList, keyWays } from "./keycaps"

// What a shortcut is drawn as. The platform is read in the browser, so the
// first render (the server's) says Ctrl; which key a Mac reader sees is the
// pure `keyWays`, read here for both keyboards.

const caps = (keys, apple) =>
  keyWays(keys, apple).map((way) =>
    way.map((part) => part.map((piece) => (piece.cap ? `[${piece.text}]` : piece.text)).join("")),
  )

describe("keyWays", () => {
  test("Ctrl/⌘ is Ctrl on a PC and ⌘ on a Mac, one cap per key", () => {
    expect(caps("Ctrl/⌘+F", false)).toEqual([["[Ctrl][F]"]])
    expect(caps("Ctrl/⌘+F", true)).toEqual([["[⌘][F]"]])
    expect(caps("Ctrl/⌘+Shift+.", true)).toEqual([["[⌘][Shift][.]"]])
  })

  test("a key that is the same on both keyboards is not changed", () => {
    expect(caps("Ctrl+C", true)).toEqual([["[Ctrl][C]"]])
    expect(caps("F5", true)).toEqual([["[F5]"]])
  })

  test("` · ` is another way, ` / ` the other half, and words stay words", () => {
    expect(caps("F5 · Ctrl/⌘+R", false)).toEqual([["[F5]"], ["[Ctrl][R]"]])
    expect(caps("↑ / ↓ · Home / End", false)).toEqual([
      ["[↑]", "[↓]"],
      ["[Home]", "[End]"],
    ])
    expect(caps("Shift+click", false)).toEqual([["[Shift]click"]])
    expect(caps("Type its first letters", false)).toEqual([["Type its first letters"]])
  })
})

describe("Keycaps", () => {
  const html = (props) => renderToStaticMarkup(h(Keycaps, props))

  test("each key is its own kbd, and the first render says Ctrl", () => {
    const out = html({ keys: "Ctrl/⌘+P" })
    expect(out.match(/<kbd/g)).toHaveLength(2)
    expect(out).toContain(">Ctrl</kbd>")
    expect(out).not.toContain("⌘")
  })

  test("a hint is not drawn on a touch screen unless it is asked for", () => {
    const hidden = (out) => /class="[^"]*\bpointer-coarse:hidden\b/.test(out)
    expect(hidden(html({ keys: "?" }))).toBe(true)
    expect(hidden(html({ keys: "?", touch: "show" }))).toBe(false)
  })
})

describe("ShortcutList", () => {
  test("lists each shortcut's words with its keys, drawn on touch too", () => {
    const out = renderToStaticMarkup(
      h(ShortcutList, {
        groups: [
          { title: "Find", items: [{ does: "Find a file", keys: "Ctrl/⌘+F" }] },
          { items: [{ does: "Show shortcuts", keys: "?" }] },
        ],
      }),
    )
    expect(out).toContain("<h3")
    expect(out.match(/<h3/g)).toHaveLength(1)
    expect(out).toMatch(/<dt>Find a file<\/dt><dd>.*<kbd[^>]*>Ctrl<\/kbd>/)
    expect(out).toContain(">?</kbd>")
    expect(out).not.toContain("pointer-coarse:hidden")
  })
})
