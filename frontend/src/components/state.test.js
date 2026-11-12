import { expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { Hold, LoadingPanel, LoadingRows } from "./state"

// A page arrives from the top: what is written under a block that still waits
// for its first answer is not drawn until the answer lands, so a block that
// lands taller than its skeleton has nothing under it to push. The marker is
// what the stylesheet reads, and eight pages moved while they loaded for the
// want of it (the audit's UI-18).
const MARK = '<span data-hold="" hidden=""></span>'

test("a hold leaves its marker, and what stands meanwhile, until the read has answered", () => {
  expect(renderToStaticMarkup(createElement(Hold, { until: false }))).toBe(MARK)
  expect(renderToStaticMarkup(createElement(Hold, { until: false }, createElement("i")))).toBe(
    `<i></i>${MARK}`,
  )
  // Answered: neither the marker nor the skeleton it stood behind.
  expect(renderToStaticMarkup(createElement(Hold, { until: true }, createElement("i")))).toBe("")
})

test("a skeleton holds what is under it only where it is asked to", () => {
  for (const skeleton of [LoadingRows, LoadingPanel]) {
    expect(renderToStaticMarkup(createElement(skeleton, {}))).not.toContain("data-hold")
    const held = renderToStaticMarkup(createElement(skeleton, { hold: true }))
    // After the skeleton, so that the skeleton itself is drawn.
    expect(held.endsWith(MARK)).toBe(true)
  }
})

test("the stylesheet takes what follows a marker, and the page's blocks under it, off the page", () => {
  const css = readFileSync(join(import.meta.dir, "../app/globals.css"), "utf8")
  const rule = css.slice(css.indexOf("[data-hold] ~ *"))
  expect(rule.slice(0, rule.indexOf("}") + 1).replace(/\s+/g, " ")).toBe(
    '[data-hold] ~ *, [data-slot="page"] > :has([data-hold]) ~ * { display: none !important; }',
  )
})
