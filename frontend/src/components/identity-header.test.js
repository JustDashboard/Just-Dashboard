import { describe, expect, mock, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"

// The verbs menu is a Radix dropdown; its trigger renders on the server, which
// is all these tests read.
import { IdentityHeader, IdentityVerbs } from "./identity-header"
import { Fact } from "./fact-line"

const draw = (props) => renderToStaticMarkup(h(IdentityHeader, props))
const noop = mock(() => {})
const Glyph = () => h("svg")

describe("IdentityHeader", () => {
  test("loading draws bones in the boxes the title, state, facts and command will take", () => {
    const html = draw({ loading: true, state: true, command: true })
    expect(html).toContain('aria-busy="true"')
    expect(html).toContain('data-layout="project"')
    // Title, state and facts: a Bone each, which keeps its line's own height.
    expect(html.match(/align-bottom/g)).toHaveLength(3)
    expect(html).toContain("w-28")
    expect(html).toContain("w-24")
    expect(html).toContain("w-64")
    // The tile and the command: skeletons the size of what lands.
    expect(html).toContain("size-10 shrink-0 rounded-lg")
    expect(html).toContain("h-(--control-compact) w-28")
  })

  test("a title already known while loading is drawn as itself", () => {
    const html = draw({ loading: true, title: "eth0", mark: "wireguard" })
    expect(html).toContain(">eth0</p>")
    expect(html.match(/align-bottom/g)).toHaveLength(1)
  })

  test("the name is the identity rung: 16px, weight 600", () => {
    const html = draw({ title: "api", facts: "x", state: "ok" })
    expect(html).toContain("text-base font-semibold")
    expect(html).not.toContain("text-title")
  })

  test("on a phone the command row is the foot by default, so the title keeps the row", () => {
    const html = draw({ title: "api.example.test", facts: "x", command: h("button", null, "View") })
    expect(html).toContain("max-sm:order-last max-sm:col-span-3")
    expect(html).not.toContain("max-sm:order-1")
  })

  test("a line with a place for verbs keeps a control's height for them, filled or not", () => {
    const empty = draw({ title: "api", facts: "x", state: "ok", menu: false })
    expect(empty).toContain("max-sm:min-h-(--control-compact)")
    const none = draw({ title: "api", facts: "x", state: "ok" })
    expect(none).not.toContain("max-sm:order-last")
  })

  test("facts written as facts are drawn as a FactLine", () => {
    const html = draw({ title: "a", facts: [h(Fact, { key: 1 }, "x"), h(Fact, { key: 2 }, "y")] })
    expect(html).toContain('data-slot="fact-line"')
  })

  test("a fallback glyph is drawn at a logo's size on the tile", () => {
    const html = draw({ title: "a", facts: "x" })
    expect(html).toContain("[&amp;&gt;svg]:size-6")
  })
})

describe("IdentityVerbs", () => {
  const verbs = [
    { key: "edit", label: "Edit", icon: Glyph, run: noop, inline: true },
    { key: "reload", label: "Reload", icon: Glyph, run: noop, inline: true },
    { key: "remove", label: "Remove", icon: Glyph, run: noop, danger: true },
  ]

  test("returns the first inline verb as the command and the rest as the menu", () => {
    const { command, menu } = IdentityVerbs({ verbs, name: "api" })
    expect(renderToStaticMarkup(command)).toContain("Edit")
    const more = renderToStaticMarkup(menu)
    expect(more).toContain('aria-label="More actions for api"')
  })

  test("no other verb, no menu", () => {
    const { command, menu } = IdentityVerbs({ verbs: verbs.slice(0, 1), name: "api" })
    expect(command).toBeTruthy()
    expect(menu).toBeFalsy()
  })
})
