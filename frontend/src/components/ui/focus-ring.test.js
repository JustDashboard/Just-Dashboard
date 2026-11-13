import { expect, test } from "bun:test"
import { readFileSync, readdirSync } from "node:fs"
import { join, relative } from "node:path"

/**
 * `focus-ring` and `focus-ring-inset` are the outline. A class that sets the
 * outline's style to none (`outline-hidden`, `outline-none`) in the same class
 * list wins over them, and the control then shows a keyboard nothing: every
 * row of the sidebar did, on every page, through a leftover of the component
 * library's own focus styling. `focus-ring-within` is not checked: the control
 * inside such a group drops its own outline on purpose and the group draws it.
 */
const SRC = join(import.meta.dir, "..", "..")
const RING = /(?<![\w:-])focus-ring(?:-inset)?(?![\w-])/
// Bare, or behind the focus variants, which switch it off exactly when it is wanted.
const NO_OUTLINE = /(?<![\w:-])(?:focus(?:-visible)?:)?outline-(?:hidden|none)(?![\w-])/

function sources(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return sources(path)
    return /\.tsx?$/.test(entry.name) ? [path] : []
  })
}

test("no class list takes the focus ring and then switches the outline off", () => {
  const found = []
  for (const path of sources(SRC)) {
    const lines = readFileSync(path, "utf8").split("\n")
    lines.forEach((line, at) => {
      if (RING.test(line) && NO_OUTLINE.test(line)) found.push(`${relative(SRC, path)}:${at + 1}`)
    })
  }
  expect(found).toEqual([])
})

test("the check reads a class list the way it is written", () => {
  const broken = (line) => RING.test(line) && NO_OUTLINE.test(line)
  expect(broken('"rounded-md p-2 focus-ring outline-hidden transition-colors"')).toBe(true)
  expect(broken('"bg-transparent focus-ring-inset outline-none"')).toBe(true)
  // The group draws the ring for the control inside it.
  expect(broken('"outline-none focus-ring-within"')).toBe(false)
  expect(broken('"focus-visible:outline-none focus-ring"')).toBe(true)
  // Another state's outline is not the keyboard's.
  expect(broken('"data-[state=open]:outline-none focus-ring"')).toBe(false)
  expect(broken('"rounded-md focus-ring"')).toBe(false)
})
