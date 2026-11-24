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

  test("the name is the identity rung: 18px on its 24px line, weight 600", () => {
    const html = draw({ title: "api", facts: "x", state: "ok" })
    expect(html).toContain("text-lg/6 font-semibold")
    expect(html).not.toContain("text-title")
  })

  test("on a phone the line is one row: the tile, the name and the verbs, then one line of facts", () => {
    const html = draw({ title: "api.example.test", facts: "x", command: h("button", null, "View") })
    // No row of its own for the verbs, at any width below the foot's.
    expect(html).not.toContain("max-sm:order-3")
    expect(html).not.toContain("max-sm:col-span-3")
    expect(html).not.toContain("max-sm:flex-wrap")
    // The tile a step smaller, the verbs 32px squares with a 44px press box.
    expect(html).toContain("max-sm:[zoom:0.8]")
    expect(html).toContain("max-sm:[&amp;_[data-size]]:hit-area")
    expect(html).toContain("max-sm:[&amp;_[data-size]]:[--control-h:2rem]")
    // A command with an icon is drawn as the icon alone; its words stay its name.
    expect(html).toContain("max-sm:[&amp;_[data-size]:has(svg)]:[font-size:0]")
    // One line of facts that never wraps to a second.
    expect(html).toContain("max-sm:h-4 max-sm:overflow-clip")
    // The command's tooltip box is no box from `sm`, so the wide line is laid out as before.
    expect(html).toContain("sm:contents")
  })

  test("a place for verbs no longer adds a row: menu={false}, a loading command and foot", () => {
    const empty = draw({ title: "api", facts: "x", state: "ok", menu: false })
    const none = draw({ title: "api", facts: "x", state: "ok" })
    expect(empty).not.toContain("max-sm:min-h-(--control-compact)")
    expect(empty).not.toContain("max-sm:mt-1")
    // The row is a control's height on a phone with no verb in it yet, whatever the tile.
    expect(empty).toContain("max-sm:min-h-8")
    expect(none).not.toContain("max-sm:mt-1")
    const footed = draw({ title: "api", facts: "x", state: "ok", menu: false, foot: true })
    // The foot's own row is a wide screen's beside a rail, never a phone's.
    expect(footed).toContain("sm:@max-2xl:order-last!")
    expect(footed).not.toMatch(/(^|[\s"])@max-2xl:order-last!/)
    const loading = draw({ loading: true, state: true, command: true })
    expect(loading).toContain("h-(--control-compact) w-28 rounded-md max-sm:size-8")
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

  test("never promotes a destructive verb, nor a menu verb in its place", () => {
    const terminate = { key: "kill", label: "Terminate", icon: Glyph, run: noop, danger: true }
    const copy = { key: "copy", label: "Copy name", icon: Glyph, run: noop }
    // The inline verb is destructive: no command, and both verbs in the menu.
    const inlineDanger = IdentityVerbs({
      verbs: [{ ...terminate, inline: true }, copy],
      name: "api",
    })
    expect(inlineDanger.command).toBeFalsy()
    expect(inlineDanger.menu).toBeTruthy()
    // Nothing marked inline and the first verb destructive: the same.
    expect(IdentityVerbs({ verbs: [terminate, copy], name: "api" }).command).toBeFalsy()
    // Only destructive verbs: no command at all.
    expect(IdentityVerbs({ verbs: [terminate], name: "api" }).command).toBeFalsy()
    // A safe inline verb after a destructive one is still the command.
    const html = renderToStaticMarkup(
      IdentityVerbs({
        verbs: [
          { ...terminate, inline: true },
          { ...copy, inline: true },
        ],
        name: "api",
      }).command,
    )
    expect(html).toContain("Copy name")
  })

  test("no other verb, no menu", () => {
    const { command, menu } = IdentityVerbs({ verbs: verbs.slice(0, 1), name: "api" })
    expect(command).toBeTruthy()
    expect(menu).toBeFalsy()
  })

  test("the command says the verb's short word and keeps its label as its name", () => {
    const logs = { key: "logs", label: "Open logs", short: "Logs", icon: Glyph, run: noop }
    const html = renderToStaticMarkup(IdentityVerbs({ verbs: [logs], name: "api" }).command)
    expect(html).toContain('aria-label="Open logs"')
    expect(html).toMatch(/>Logs<\/button>/)
    // Without a short word the label is the words and the name both.
    const plain = renderToStaticMarkup(IdentityVerbs({ verbs, name: "api" }).command)
    expect(plain).not.toContain("aria-label")
    expect(plain).toMatch(/>Edit<\/button>/)
  })
})
