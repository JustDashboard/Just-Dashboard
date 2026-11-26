import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import { tabClasses, ViewTabs } from "./tabs"
import { Segments } from "./segments"
import { FilterChip } from "./chips"
import { ToggleGroup, ToggleGroupItem } from "./ui/toggle-group"
import { Tabs, TabsContent } from "./ui/tabs"

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

describe("a strip's tab semantics", () => {
  const strip = (views, extra) =>
    renderToStaticMarkup(
      h(
        ViewTabs,
        { label: "Views of api", views, value: views[0].key, onValueChange: () => {}, ...extra },
        h(TabsContent, { value: views[0].key }, "the view"),
      ),
    )
  const attr = (html, name) =>
    [...html.matchAll(new RegExp(`${name}="([^"]*)"`, "g"))].map((m) => m[1])

  test("every panel is labelled by a tab that is there", () => {
    const html = strip(VIEWS)
    expect(html).toContain('role="tablist"')
    expect(html).toContain('role="tabpanel"')
    const labelledBy = attr(html, "aria-labelledby")
    expect(labelledBy).toHaveLength(1)
    expect(attr(html, "id")).toContain(labelledBy[0])
  })

  test("one view draws no strip, and its panel is a plain box labelled by nothing missing", () => {
    const html = strip([{ key: "grants", label: "Roles" }], { hideWhenSingle: true })
    expect(html).not.toContain('role="tablist"')
    expect(html).not.toContain('role="tab"')
    expect(html).not.toContain('role="tabpanel"')
    // Not a second landmark round what the view holds, which names itself.
    expect(html).not.toContain("role=")
    expect(html).not.toContain("aria-label")
    expect(html).not.toContain("tabindex")
    expect(html).toContain("the view")
  })

  test("one view without hideWhenSingle keeps its tab, which labels the panel", () => {
    const html = strip([{ key: "grants", label: "Roles" }])
    expect(html).toContain('role="tab"')
    expect(attr(html, "id")).toContain(attr(html, "aria-labelledby")[0])
  })

  test("a strip of its own inside a lone view's panel is tabs again", () => {
    const html = renderToStaticMarkup(
      h(
        ViewTabs,
        {
          label: "Views of api",
          views: [{ key: "grants", label: "Roles" }],
          value: "grants",
          onValueChange: () => {},
          hideWhenSingle: true,
        },
        h(
          TabsContent,
          { value: "grants" },
          h(Tabs, { value: "inner" }, h(TabsContent, { value: "inner" }, "inner")),
        ),
      ),
    )
    // The inner one is a tab panel again; the lone one round it is not.
    expect(html.match(/role="tabpanel"/g)).toHaveLength(1)
    expect(html.match(/aria-labelledby="[^"]*"/g)).toHaveLength(1)
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
