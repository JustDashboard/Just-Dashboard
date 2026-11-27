import { describe, expect, test } from "bun:test"
import { readdirSync, readFileSync } from "node:fs"
import { join, relative } from "node:path"

// Menus are words (standard.md, 27 Nov 2026): a menu item says its verb as a
// word, with no glyph in front of it. The operator asked for it product-wide
// after a terminal's ⋯ menu drew a glyph beside each of its five words. A
// menu's items are only in the document while it is open, so no browser spec
// sees every one; this reads the source instead, and names each item that
// draws a glyph first.
//
// What an item may still draw is what it is rather than what it does: a tick
// for the current choice, a colour's swatch, a status dot, an avatar, and in
// a list of things (servers, containers, places) each one's own mark, which
// tells them apart where a verb's glyph only decorated it. Those do not come
// from the glyph modules below, or are named in KEPT with the reason.

const SRC = join(import.meta.dir, "../..")
const GLYPH_MODULES = new Set([
  "@/components/icons",
  "@/components/git/glyphs",
  "@/components/boards/glyphs",
])
const ITEM =
  /<(DropdownMenuItem|DropdownMenuSubTrigger|DropdownMenuCheckboxItem|DropdownMenuRadioItem|ContextMenuItem|ContextMenuSubTrigger|ContextMenuCheckboxItem|ContextMenuRadioItem)\b/g
/**
 * A glyph handed in or looked up rather than imported: `<verb.icon />`,
 * `<Icon />`, `<KindIcon />`, `<OptionMark />`.
 */
const DRAWN = /(^|\.)(icon|Icon)$|[a-z]Icon$|Glyph$|Mark$/
/** Glyphs that say which choice is current or that one is in flight, not what the verb is. */
const STATE = new Set(["Check", "Spinner"])
/** A glyph that is the thing an item names, in a list of such things: `file` → glyph → why. */
const KEPT = {
  "components/hub-link/server-switcher.tsx": {
    LogoGlyph: "Just Hub's own mark, among servers that each draw a status dot",
  },
  "components/network/traffic/workloads.tsx": {
    ProductGlyph: "each container's product, the mark its strip draws it by",
  },
  "components/files/places-menu.tsx": {
    PlaceMark: "each place as its folder is drawn: its colour, or the system's logo for /",
  },
  "components/files/trash-place.tsx": {
    Trash: "the Trash as a place, among places that each draw their folder",
  },
  "components/assistant/agent/mode-menu.tsx": {
    OptionMark: "the lock the toolbar shows for each permission, full access in amber",
  },
}

function sources(dir, out = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) sources(path, out)
    else if (entry.name.endsWith(".tsx")) out.push(path)
  }
  return out
}

function glyphNames(source) {
  const names = new Set()
  for (const [, list, from] of source.matchAll(/import\s*\{([^}]*)\}\s*from\s*"([^"]+)"/g)) {
    if (!GLYPH_MODULES.has(from)) continue
    for (const part of list.split(",")) {
      const name = part
        .trim()
        .replace(/^type\s+/, "")
        .split(/\s+as\s+/)
        .pop()
      if (name && /^[A-Z]/.test(name) && !STATE.has(name)) names.add(name)
    }
  }
  return names
}

/** Where a JSX opening tag ends: its first `>` outside braces and strings, not an arrow's. */
function tagEnd(source, from) {
  let depth = 0
  for (let i = from; i < source.length; i++) {
    const c = source[i]
    if (c === "{") depth++
    else if (c === "}") depth--
    else if (c === '"' && depth === 0) i = source.indexOf('"', i + 1)
    else if (c === ">" && depth === 0 && source[i - 1] !== "=") {
      return { end: i, closed: source[i - 1] === "/" }
    }
  }
  return null
}

/** The end of a `{…}` expression starting at `from`. */
function braceEnd(source, from) {
  let depth = 0
  for (let i = from; i < source.length; i++) {
    if (source[i] === "{") depth++
    else if (source[i] === "}" && --depth === 0) return i
  }
  return source.length
}

/**
 * The glyph an item's content starts with, if it starts with one: its first
 * child, or the first child of a wrapping element (a link, a text column), or
 * either arm of a condition in that place.
 */
function leadingGlyph(source, at, glyphs, depth = 0) {
  const rest = source.slice(at)
  const skipped = rest.length - rest.trimStart().length
  const start = at + skipped
  if (source.startsWith("{", start)) {
    const expression = source.slice(start, braceEnd(source, start) + 1)
    if (/^\{\s*\/\*/.test(expression))
      return leadingGlyph(source, start + expression.length, glyphs, depth)
    for (const [, name] of expression.matchAll(/<([A-Z][A-Za-z0-9.]*)/g)) {
      if (glyphs.has(name) || DRAWN.test(name)) return name
    }
    return null
  }
  const element = /^<([A-Za-z][A-Za-z0-9.]*)/.exec(source.slice(start))
  if (!element) return null
  if (glyphs.has(element[1]) || DRAWN.test(element[1])) return element[1]
  const tag = tagEnd(source, start + 1)
  if (!tag || tag.closed || depth >= 3) return null
  return leadingGlyph(source, tag.end + 1, glyphs, depth + 1)
}

function offenders() {
  const found = []
  for (const path of sources(SRC)) {
    const file = relative(SRC, path)
    if (file.startsWith("components/ui/")) continue
    const source = readFileSync(path, "utf8")
    const glyphs = glyphNames(source)
    for (const match of source.matchAll(ITEM)) {
      const tag = tagEnd(source, match.index + 1)
      if (!tag || tag.closed) continue
      const glyph = leadingGlyph(source, tag.end + 1, glyphs)
      if (!glyph || KEPT[file]?.[glyph]) continue
      const line = source.slice(0, match.index).split("\n").length
      found.push(`${file}:${line} <${match[1]}> starts with <${glyph}>`)
    }
  }
  return found
}

describe("menus are words", () => {
  test("no menu item draws a glyph in front of its word", () => {
    expect(offenders()).toEqual([])
  })
})
