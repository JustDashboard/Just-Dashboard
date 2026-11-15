import { describe, expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { TileTrend, buildPath } from "./sparkline"

const level = (path) => Number(path.line.split(",").at(-1))

describe("a steady series", () => {
  test("draws a level line through the middle on a scale of its own, with no area", () => {
    const path = buildPath([12, 12, 12, 12], undefined, 240, 36)
    expect(path).not.toBeNull()
    expect(path.fill).toBeUndefined()
    expect(level(path)).toBe(18)
    expect(path.line).toBe("M0,18.00L240.00,18.00")
  })

  test("of nothing at all stays on the floor", () => {
    expect(level(buildPath([0, 0, 0], undefined, 240, 36))).toBe(35)
  })

  test("against a fixed ceiling sits at its level, as a line rather than a block", () => {
    const path = buildPath([99, 99, 99], 100, 240, 36)
    expect(path.fill).toBeUndefined()
    expect(level(path)).toBeCloseTo(1 + 34 - 0.99 * 34, 2)
  })

  test("is drawn in a tile's trend slot rather than left out", () => {
    const html = renderToStaticMarkup(
      createElement(TileTrend, { values: [5, 5, 5], label: "Sessions over the last hour" }),
    )
    expect(html).toContain("<svg")
    expect(html).toContain('aria-label="Sessions over the last hour"')
    expect(html).not.toContain("url(#")
  })
})

describe("a moving series", () => {
  test("keeps its area under the line", () => {
    const path = buildPath([1, 4, 2], undefined, 64, 20)
    expect(path.fill).toBeDefined()
  })

  test("is not a shape below two points", () => {
    expect(renderToStaticMarkup(createElement(TileTrend, { values: [3], label: "x" }))).toBe("")
  })
})
