import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Panel, PanelHeader, PaneSection } from "./panel"
import { Section } from "./section"
import { FormSection } from "./form"

// The section head's contract, read from its markup: one look at every level,
// the count and the way on at the end of the line, outside the heading.

const classOf = (html, tag) => html.match(new RegExp(`<${tag} class="([^"]*)"`))?.[1]

describe("PanelHeader", () => {
  test("level 3 renders an h3 with the h2's classes", () => {
    const two = renderToStaticMarkup(h(PanelHeader, { title: "Ports" }))
    const three = renderToStaticMarkup(h(PanelHeader, { title: "Ports", level: 3 }))
    expect(three).toContain("<h3")
    expect(three).not.toContain("<h2")
    expect(classOf(three, "h3")).toBe(classOf(two, "h2"))
  })

  test("the title is the surface title at 600", () => {
    const html = renderToStaticMarkup(h(PanelHeader, { title: "Ports" }))
    expect(classOf(html, "h2")).toContain("text-title")
    expect(classOf(html, "h2")).toContain("font-semibold")
    expect(classOf(html, "h2")).not.toContain("font-medium")
  })

  test("the count is numeric hint text at the end, before the actions, outside the heading", () => {
    const html = renderToStaticMarkup(
      h(PanelHeader, { title: "Ports", count: 12, actions: h("button", null, "Add") }),
    )
    const count = html.match(/<span data-slot="panel-count" class="([^"]*)">12<\/span>/)
    expect(count).toBeTruthy()
    expect(count[1]).toContain("numeric")
    expect(count[1]).toContain("text-hint")
    expect(html.indexOf("panel-count")).toBeGreaterThan(html.indexOf("</h2>"))
    expect(html.indexOf("panel-count")).toBeLessThan(html.indexOf("<button"))
  })

  test("a count of zero is drawn; no count draws no end", () => {
    expect(renderToStaticMarkup(h(PanelHeader, { title: "Ports", count: 0 }))).toContain(
      ">0</span>",
    )
    expect(renderToStaticMarkup(h(PanelHeader, { title: "Ports" }))).not.toContain("panel-count")
  })

  test("the link is a SectionLink, last at the end", () => {
    const html = renderToStaticMarkup(
      h(PanelHeader, {
        title: "Resources",
        link: { href: "/metrics", label: "All metrics" },
        actions: h("button", null, "Refresh"),
      }),
    )
    expect(html).toMatch(
      /<a[^>]*href="\/metrics"[^>]*data-slot="section-link"|<a[^>]*data-slot="section-link"[^>]*href="\/metrics"/,
    )
    expect(html).toContain("hit-area")
    expect(html).toContain("All metrics")
    expect(html.indexOf("section-link")).toBeGreaterThan(html.indexOf("<button"))
  })

  test("plain draws the plain head without a panel around it", () => {
    const html = renderToStaticMarkup(h(PanelHeader, { title: "Facts", plain: true }))
    expect(html).toContain("px-0")
    expect(html).toContain("pb-3")
    expect(html).toContain("border-hairline")
  })
})

describe("Section and FormSection draw the one head", () => {
  test("Section's head is the plain PanelHeader, with its count", () => {
    const html = renderToStaticMarkup(h(Section, { title: "Resources", count: 4 }, "body"))
    expect(html).toContain('data-slot="panel-header"')
    expect(classOf(html, "h2")).toContain("font-semibold")
    expect(classOf(html, "h2")).toContain("text-title")
    expect(html).toContain("border-b border-hairline")
    expect(html).toContain('data-slot="panel-count"')
  })

  test("FormSection is an h3 in a dialog and an h2 as a page's section", () => {
    const part = renderToStaticMarkup(h(FormSection, { title: "Ports" }, "fields"))
    const page = renderToStaticMarkup(h(FormSection, { title: "Build", aside: true }, "fields"))
    expect(part).toContain('data-slot="panel-header"')
    expect(part).toContain("<h3")
    expect(page).toContain("<h2")
    expect(classOf(part, "h3")).toBe(classOf(page, "h2"))
  })
})

describe("Panel", () => {
  test("framed says the frame; plain and framed are opposite", () => {
    expect(renderToStaticMarkup(h(Panel, null))).toContain("data-framed")
    expect(renderToStaticMarkup(h(Panel, { plain: true }))).toContain("data-plain")
    const framed = renderToStaticMarkup(h(Panel, { framed: true }))
    expect(framed).toContain("data-framed")
    expect(framed).toContain("rounded-xl border")
    const off = renderToStaticMarkup(h(Panel, { framed: false }))
    expect(off).toContain("data-plain")
    expect(off).not.toContain("rounded-xl")
  })

  test("a frame drops its sides on a phone page, except a panel you press", () => {
    expect(renderToStaticMarkup(h(Panel, null))).toContain("in-data-[slot=page]:max-sm:-mx-5")
    expect(renderToStaticMarkup(h(Panel, { interactive: true }))).not.toContain("max-sm:-mx-5")
    expect(renderToStaticMarkup(h(Panel, { plain: true }))).not.toContain("max-sm:-mx-5")
  })
})

describe("PaneSection", () => {
  test("an eyebrow h3 named for its region, with its count and aside", () => {
    const html = renderToStaticMarkup(
      h(PaneSection, { title: "Ports", count: 3, aside: h("button", null, "Copy") }, "rows"),
    )
    expect(html).toContain('aria-label="Ports"')
    expect(html).toMatch(/<h3[^>]*><span class="eyebrow[^"]*">Ports<\/span>/)
    expect(html).toContain(">3</span>")
    expect(html).toContain("last:border-b-0")
    expect(html).toContain("<button>Copy</button>")
  })
})
