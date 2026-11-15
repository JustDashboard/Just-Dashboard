import { describe, expect, test } from "bun:test"
import { Children, cloneElement, createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { TooltipProvider } from "@/components/ui/tooltip"
import { DANGER_AT_REST, VerbActions, VerbItemList } from "./verbs"

// What a verb is drawn as, read from the markup of a first render. A menu's
// items are only in the document while it is open, through a portal, so the
// list is drawn here with stand-in parts that print what they are handed; the
// open menu itself is held in tests/browser/shared-dropdowns.spec.ts.

const Glyph = ({ className }) => h("svg", { className, "data-glyph": "" })

// A menu's handler is the library's to call; the stand-in prints the rest.
const part =
  (name) =>
  ({ asChild, children, variant, ...props }) => {
    delete props.onSelect
    return asChild
      ? cloneElement(Children.only(children), { "data-part": name, "data-variant": variant })
      : h("div", { "data-part": name, "data-variant": variant, ...props }, children)
  }

const PARTS = {
  Item: part("item"),
  Label: part("label"),
  Separator: part("separator"),
  Shortcut: part("shortcut"),
  Sub: part("sub"),
  SubTrigger: part("sub-trigger"),
  SubContent: part("sub-content"),
}

const items = (verbs) => renderToStaticMarkup(h(VerbItemList, { verbs, parts: PARTS }))
// The shell's provider, which every IconAction's tooltip reads.
const draw = (props) => renderToStaticMarkup(h(TooltipProvider, null, h(VerbActions, props)))
const actions = (verbs) => draw({ verbs })
const classesOf = (html, label) =>
  (html.match(new RegExp(`<button[^>]*aria-label="${label}"[^>]*>`))?.[0] ?? "")
    .match(/class="([^"]*)"/)?.[1]
    .split(/\s+/) ?? []

describe("VerbItems", () => {
  test("a verb with an address is a link, through the router when it is internal", () => {
    const html = items([
      { key: "logs", label: "Logs", icon: Glyph, href: "/docker/logs", internal: true },
      { key: "dump", label: "Download", icon: Glyph, href: "/api/dump", download: "dump.sql" },
    ])
    // The item's own props reach the link, or the arrow keys would skip it.
    expect(html).toMatch(/<a(?=[^>]*href="\/docker\/logs")(?=[^>]*data-part="item")[^>]*>/)
    expect(html).toMatch(/<a(?=[^>]*href="\/api\/dump")(?=[^>]*download="dump.sql")[^>]*>/)
    expect(html).toContain(">Logs</span>")
  })

  test("a checked verb is a menuitemcheckbox that says which one is current", () => {
    const html = items([
      { key: "a", label: "Name", icon: Glyph, checked: true, run: () => {} },
      { key: "b", label: "Size", icon: Glyph, checked: false, run: () => {} },
    ])
    expect(html).toMatch(/role="menuitemcheckbox" aria-checked="true"[^>]*>.*Name/)
    expect(html).toMatch(/role="menuitemcheckbox" aria-checked="false"[^>]*>.*Size/)
    // The tick is drawn for the current one only.
    expect(html.match(/data-glyph/g)?.length).toBe(2)
    expect(html.match(/<svg/g)?.length).toBe(3)
  })

  test("a verb with a submenu opens one level down with its verbs inside", () => {
    const html = items([
      {
        key: "colour",
        label: "Colour",
        icon: Glyph,
        submenu: [
          { key: "red", label: "Red", icon: Glyph, checked: false, run: () => {} },
          { key: "blue", label: "Blue", icon: Glyph, checked: true, run: () => {} },
        ],
      },
    ])
    expect(html).toMatch(
      /data-part="sub".*data-part="sub-trigger".*Colour.*data-part="sub-content".*Red.*Blue/,
    )
  })

  test("groups open under their names and danger under a hairline", () => {
    const html = items([
      { key: "start", label: "Start", icon: Glyph, group: "Run", run: () => {} },
      { key: "edit", label: "Edit", icon: Glyph, group: "Project", run: () => {} },
      { key: "rm", label: "Remove", icon: Glyph, group: "Project", danger: true, run: () => {} },
    ])
    expect(html.match(/data-part="label"/g)?.length).toBe(2)
    expect(html.match(/data-part="separator"/g)?.length).toBe(2)
    expect(html).toMatch(/data-variant="destructive"[^>]*>.*Remove/)
  })
})

describe("VerbActions", () => {
  test("an inline danger verb is grey at rest at every width, red only when pressed for", () => {
    for (const mode of ["plain", "reveal", "dim"]) {
      const html = draw({
        mode,
        verbs: [{ key: "rm", label: "Delete dump", icon: Glyph, inline: true, danger: true }],
      })
      const classes = classesOf(html, "Delete dump")
      for (const token of DANGER_AT_REST.split(" ")) expect(classes).toContain(token)
      // No bare destructive ink: no breakpoint or hover rule is what keeps it grey.
      expect(classes).not.toContain("text-destructive")
      expect(classes.some((c) => /^(sm|md|lg|max-sm|max-md):text-/.test(c))).toBe(false)
    }
  })

  test("an inline verb with an address is a link", () => {
    const html = actions([
      { key: "open", label: "Open", icon: Glyph, inline: true, href: "/x", internal: true },
    ])
    expect(html).toMatch(/<a(?=[^>]*aria-label="Open")(?=[^>]*href="\/x")[^>]*>/)
  })

  test("the old boolean modes still pick the same wrapper as mode", () => {
    const verbs = [{ key: "a", label: "Restart", icon: Glyph, inline: true, run: () => {} }]
    const byMode = draw({ verbs, mode: "dim" })
    const byFlag = draw({ verbs, dim: true })
    expect(byFlag).toBe(byMode)
  })
})
