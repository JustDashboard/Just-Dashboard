import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import { tabClasses, ViewTabs } from "./tabs"
import { Segments } from "./segments"
import { FilterChip } from "./chips"
import { ToggleGroup, ToggleGroupItem } from "./ui/toggle-group"

// Just Hub's tabs (components/tabs-chips-segments.md, "The look, from Just
// Hub"): the chosen tab is a face, never an underline, and a segmented control
// is one track with the chosen fill in it, under no outline.

const VIEWS = [
  { key: "traffic", label: "Traffic" },
  { key: "addresses", label: "Addresses", count: 3 },
]

describe("a quiet tab", () => {
  test("the chosen one is a control face, with no underline", () => {
    const chosen = tabClasses(true, "h-10")
    expect(chosen).toContain("bg-control")
    expect(chosen).toContain("text-foreground")
    expect(chosen).not.toMatch(/border-b-2|border-brand|after:/)
  })

  test("at rest it is the muted ink, and the pointer gives it a face", () => {
    const rest = tabClasses(false, "h-10")
    expect(rest).toContain("text-muted-foreground")
    expect(rest).toContain("hover:bg-accent")
    expect(rest).not.toContain("bg-control")
  })

  test("the face sits 4px inside the tab, which keeps its strip's height and an inset ring", () => {
    const tab = tabClasses(true, "h-10")
    expect(tab).toContain("h-10")
    expect(tab).toContain("border-4 border-transparent bg-clip-padding")
    expect(tab).toContain("rounded-[calc(var(--radius-md)+4px)]")
    expect(tab).toContain("focus-ring-inset")
  })

  test("ViewTabs draws the same face from Radix's own state", () => {
    const html = renderToStaticMarkup(
      h(ViewTabs, {
        label: "Views of ens3",
        views: VIEWS,
        value: "traffic",
        onValueChange: () => {},
      }),
    )
    expect(html).toContain("data-[state=active]:bg-control")
    expect(html).toContain("data-[state=inactive]:hover:bg-accent")
    expect(html).not.toMatch(/border-b-2|border-brand/)
  })
})

describe("a segmented control", () => {
  test("is one track, with each chosen segment its own fill until the sliding one is measured", () => {
    const html = renderToStaticMarkup(
      h(Segments, {
        label: "Severity",
        value: "warning",
        onChange: () => {},
        options: [
          { value: "warning", label: "Warning" },
          { value: "critical", label: "Critical" },
        ],
      }),
    )
    expect(html).toContain("border-hairline bg-control p-0.75")
    expect(html).toContain("data-[state=on]:bg-chosen")
    // No line round the answer: it was louder than the answer.
    expect(html).not.toMatch(/data-\[state=on\]:(border-border-strong|bg-popover)/)
    // 32px: a 24px segment in the track's 1px line and 3px of padding.
    expect(html).toContain("h-6 pointer-coarse:h-8")
    expect(html).not.toContain("data-face")
  })

  test("inline keeps its 24px with a 2px track round an 18px segment", () => {
    const html = renderToStaticMarkup(
      h(Segments, {
        label: "Compare with",
        size: "inline",
        value: "current",
        onChange: () => {},
        options: [
          { value: "current", label: "Now" },
          { value: "previous", label: "Before it" },
        ],
      }),
    )
    expect(html).toContain("p-0.5")
    expect(html).not.toContain("p-0.75")
    expect(html).toContain("h-4.5")
  })

  test("a group with spacing stays separate controls, not a track", () => {
    const html = renderToStaticMarkup(
      h(
        ToggleGroup,
        { type: "single", variant: "outline", size: "sm", spacing: 1, "aria-label": "Runs" },
        h(ToggleGroupItem, { value: "hourly" }, "Hourly"),
      ),
    )
    expect(html).not.toContain("p-0.75")
    expect(html).toContain('data-spacing="1"')
  })
})

describe("a filter chip", () => {
  test("chosen, it trades its line for the chosen fill", () => {
    const chosen = renderToStaticMarkup(h(FilterChip, { selected: true }, "Errors"))
    expect(chosen).toContain("border-transparent bg-chosen text-foreground")
    expect(chosen).not.toMatch(/border-border-strong|bg-accent/)
    const rest = renderToStaticMarkup(h(FilterChip, { selected: false }, "Errors"))
    expect(rest).toContain("border-input")
    expect(rest).not.toContain("bg-chosen")
  })
})
