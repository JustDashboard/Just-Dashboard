import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { CHOICE_ROW_BOX, CONTROL, ChoiceListSkeleton, choicePress } from "./choice-row"

// A press, as `choicePress` reads it: where it landed, the surface handling it
// and the modifiers held. `inside` lists the selectors the target is under.
function element(name, { inside = [], contained = true } = {}) {
  return {
    name,
    closest: (selector) =>
      selector === CONTROL && inside.length > 0 ? { name: inside[0] } : null,
    contains: () => contained,
  }
}

function press(target, surface, keys = {}) {
  return {
    target,
    currentTarget: surface,
    metaKey: false,
    ctrlKey: false,
    shiftKey: false,
    ...keys,
  }
}

describe("choicePress", () => {
  const surface = element("surface")

  test("a plain press on the surface or its words takes the choice", () => {
    expect(choicePress(press(surface, surface), { href: "/deploy/7" })).toBe("go")
    expect(choicePress(press(element("words"), surface), {})).toBe("go")
  })

  for (const key of ["metaKey", "ctrlKey", "shiftKey"]) {
    test(`${key} opens a destination in a new tab`, () => {
      expect(
        choicePress(press(element("words"), surface, { [key]: true }), { href: "/git?repo=a" }),
      ).toBe("tab")
    })
  }

  test("a modifier on a choice with nowhere to go still chooses in place", () => {
    expect(choicePress(press(surface, surface, { metaKey: true }), {})).toBe("go")
  })

  test("a press on a control inside the surface is that control's", () => {
    for (const control of ["button", "a", "[role='switch']", "[role='menuitem']"]) {
      const target = element("glyph", { inside: [control] })
      expect(choicePress(press(target, surface), { href: "/x" })).toBe("ignore")
      expect(choicePress(press(target, surface, { metaKey: true }), { href: "/x" })).toBe("ignore")
    }
  })

  test("a press carried up from a portal that is not on the surface is ignored", () => {
    const portal = element("menu label", { contained: false })
    const outside = { ...surface, contains: () => false }
    expect(choicePress(press(portal, outside), { href: "/x" })).toBe("ignore")
  })

  test("a press that ends a text selection is a selection", () => {
    expect(choicePress(press(surface, surface), { href: "/x", selection: "/srv/app" })).toBe(
      "ignore",
    )
  })

  test("a disabled choice takes no press", () => {
    expect(choicePress(press(surface, surface), { disabled: true })).toBe("ignore")
  })
})

describe("ChoiceListSkeleton", () => {
  const cards = (html) => html.match(/<li[^>]*>[\s\S]*?<\/li>/g) ?? []

  test("each ghost card is ChoiceRow's 58px box: its 1px edge around the row's 56px", () => {
    // The row's box is min-h-14 (56px) inside a border on a card of its own.
    expect(CHOICE_ROW_BOX.split(" ")).toContain("min-h-14")
    expect(CHOICE_ROW_BOX.split(" ")).toContain("py-2.5")
    const html = renderToStaticMarkup(h(ChoiceListSkeleton, { rows: 3 }))
    expect(cards(html)).toHaveLength(3)
    for (const card of cards(html)) {
      const [, edge] = card.match(/^<li class="([^"]*)"/)
      expect(edge.split(" ")).toContain("border")
      expect(edge.split(" ")).toContain("rounded-xl")
      expect(edge.split(" ")).not.toContain("min-h-14")
      expect(card).toContain(`class="${CHOICE_ROW_BOX} items-center gap-3"`)
    }
  })

  test("keeps the skeleton slot specs wait on, and stays out of the accessibility tree", () => {
    const html = renderToStaticMarkup(h(ChoiceListSkeleton, { rows: 2, lines: 2 }))
    expect(html).toStartWith('<ul aria-hidden="true"')
    expect(html).toContain('data-slot="skeleton"')
  })

  test("the mark slot takes the size the row's leading mark does", () => {
    expect(renderToStaticMarkup(h(ChoiceListSkeleton, { rows: 1 }))).toContain("size-8")
    expect(renderToStaticMarkup(h(ChoiceListSkeleton, { rows: 1, mark: "md" }))).toContain(
      "size-10",
    )
    expect(renderToStaticMarkup(h(ChoiceListSkeleton, { rows: 1, mark: false }))).not.toContain(
      "size-8",
    )
  })

  test("no rows draws nothing: the empty slab is the caller's", () => {
    expect(renderToStaticMarkup(h(ChoiceListSkeleton, { rows: 0 }))).toBe("")
  })
})
