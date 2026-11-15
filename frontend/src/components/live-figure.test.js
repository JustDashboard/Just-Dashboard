import { describe, expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { LiveBytes, LiveFigure } from "./live-figure"

describe("LiveFigure", () => {
  test("keeps the space before its unit inside a flex parent", () => {
    // A leading space in a text node that is a flex item collapses; a
    // no-break space in the unit's own span does not.
    const html = renderToStaticMarkup(
      createElement("span", { className: "flex" }, createElement(LiveBytes, { value: 31.4e9 })),
    )
    expect(html).toMatch(/<span data-slot="unit"> [KMGT]?B<\/span>/)
    // Not an ordinary space, which is the one that collapses.
    expect(html).not.toMatch(/> [KMGT]?B</)
  })

  test("sets a unit written against the number against it", () => {
    const html = renderToStaticMarkup(createElement(LiveFigure, { value: 42, unit: "%" }))
    expect(html).toContain('<span data-slot="unit">%</span>')
  })

  test("draws no unit span without a unit", () => {
    const html = renderToStaticMarkup(createElement(LiveFigure, { value: 3 }))
    expect(html).not.toContain('data-slot="unit"')
  })
})
