import { describe, expect, test } from "bun:test"
import { Fragment, createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Fact, FactDot, FactLine, facts, hasFacts } from "./fact-line"

// What a line of facts says, read as `textContent` would: the 68 specs that
// read a header's facts read them this way.
const text = (html) =>
  html
    .replace(/<[^>]+>/g, "")
    .replace(/&#x27;/g, "'")
    .replace(/&amp;/g, "&")
const draw = (...children) => renderToStaticMarkup(h(FactLine, null, ...children))

describe("FactLine", () => {
  test("textContent keeps ' · ' between facts, and none before the first", () => {
    const html = draw(h(Fact, null, "3 sites"), h(Fact, null, "2 on TLS"), h(Fact, null, "eu"))
    expect(text(html)).toBe("3 sites · 2 on TLS · eu")
  })

  test("the dot is hidden from assistive technology and drawn in the faint ink", () => {
    const html = draw(h(Fact, null, "a"), h(Fact, null, "b"))
    expect(html).toContain(
      'aria-hidden="true" class="absolute inset-y-0 left-0 w-4 text-center text-faint"',
    )
    expect(html).not.toMatch(/muted-foreground\/\d+/)
  })

  test("a fact's title is on its own words, not on the dot before it", () => {
    const html = draw(h(Fact, null, "a"), h(Fact, { title: "basis" }, "2.1 a week"))
    expect(html).toContain('<span title="basis">2.1 a week</span>')
  })

  test("facts inside fragments and behind conditions are flattened in order", () => {
    const html = draw(
      h(Fact, null, "a"),
      false,
      h(Fragment, null, h(Fact, null, "b"), null, h(Fact, null, "c")),
    )
    expect(text(html)).toBe("a · b · c")
  })

  test("a line still written with FactDot reads the same, each run between dots one fact", () => {
    const html = draw(
      h("span", null, "member since May"),
      h(FactDot),
      h("span", null, "signed in"),
      " ",
      h("span", null, "now"),
      h(Fragment, null, h(FactDot), h("span", null, "Firefox")),
    )
    expect(text(html).replace(/\s+/g, " ")).toBe("member since May · signed in now · Firefox")
    expect(html.match(/data-slot="fact"/g)).toHaveLength(3)
    expect(html).not.toContain(">·</span>")
  })

  test("nowrap keeps the line on one row and lets its facts truncate", () => {
    const html = renderToStaticMarkup(h(FactLine, { nowrap: true }, h(Fact, null, "a")))
    expect(html).toContain("flex-nowrap")
    expect(html).not.toContain("flex-wrap")
  })

  test("facts() makes a Fact of each entry that says something", () => {
    const html = draw(facts(["one", false, null, "", "two"]))
    expect(text(html)).toBe("one · two")
  })

  test("hasFacts tells a run of facts from anything else", () => {
    expect(hasFacts(h(Fragment, null, h("span", null, "a"), h(FactDot)))).toBe(true)
    expect(hasFacts([h(Fact, { key: 1 }, "a")])).toBe(true)
    expect(hasFacts(h("span", null, "a"))).toBe(false)
  })
})
