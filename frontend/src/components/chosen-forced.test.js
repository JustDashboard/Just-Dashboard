import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import { FilterChip } from "./chips"
import { Segments } from "./segments"
import { Tabs, TabsList, TabsTrigger } from "./ui/tabs"

// Forced colours (Windows High Contrast) repaint every fill as the page and
// every ink as the text's, so a chosen switch read as any other. Each switcher
// takes the system's selection there, as the page's tabs do
// (`tabs.test.js`), and only there: normal colours keep the face with no
// outline (components/tabs-chips-segments.md).

const KEPT = ["forced-colors:text-[HighlightText]", "forced-colors:forced-color-adjust-none"]
const normalOutlines = (classes) =>
  classes.split(/\s+/).filter((one) => /(^|:)outline-/.test(one) && !one.includes("forced-colors:"))

describe("under forced colours the chosen one is the system's selection", () => {
  test("a filter chip, its ring outside it on the page", () => {
    const html = (selected) => renderToStaticMarkup(h(FilterChip, { selected }, "Failed"))
    for (const one of [...KEPT, "forced-colors:bg-[Highlight]", "forced-colors:[--ring:Highlight]"])
      expect(html(true)).toContain(one)
    expect(html(false)).not.toContain("Highlight")
    expect(normalOutlines(html(true))).toEqual([])
  })

  test("a segment, its ring inside its face", () => {
    const html = renderToStaticMarkup(
      h(Segments, {
        label: "Group by",
        value: "engine",
        onChange: () => {},
        options: [
          { value: "engine", label: "Engine" },
          { value: "where", label: "Where it runs" },
        ],
      }),
    )
    for (const one of [
      ...KEPT,
      "forced-colors:bg-[Highlight]",
      // Inset, on a Highlight face: a Highlight ring would not be seen.
      "forced-colors:[--ring:HighlightText]",
    ])
      expect(html).toContain(`data-[state=on]:${one}`)
    expect(html).not.toMatch(/data-\[state=off\]:forced-colors/)
    expect(normalOutlines(html)).toEqual([])
  })

  test("a tab of the shared kit, in either look, its ring inside the face", () => {
    for (const variant of ["default", "line"]) {
      const html = renderToStaticMarkup(
        h(
          Tabs,
          { value: "preview" },
          h(
            TabsList,
            { variant },
            h(TabsTrigger, { value: "preview" }, "Preview"),
            h(TabsTrigger, { value: "checks" }, "Checks"),
          ),
        ),
      )
      // Said per look, to outweigh each look's own chosen fill.
      expect(html).toContain(
        `group-data-[variant=${variant}]/tabs-list:data-[state=active]:forced-colors:bg-[Highlight]`,
      )
      for (const one of [...KEPT, "forced-colors:[--ring:HighlightText]"])
        expect(html).toContain(`data-[state=active]:${one}`)
      expect(normalOutlines(html)).toEqual([])
    }
  })
})
