import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Field, FieldCheck, FieldChecks, useField, useFieldControl } from "./form"
import { Input } from "./ui/input"
import { Select, SelectTrigger, SelectValue } from "./ui/select"
import { Segments } from "./segments"

// What a Field hands its control, read from the markup of a first render. The
// controls themselves (Input, Textarea, SelectTrigger) read it through
// useFieldControl; a probe stands in for them here so the contract is held
// whatever a primitive draws around its <input>.

function Probe(props) {
  return h("input", useFieldControl({ "data-probe": "", ...props }))
}

function GroupProbe() {
  const field = useField()
  return h("div", { role: "radiogroup", "aria-labelledby": field?.labelId })
}

const attr = (html, selector, name) => {
  const tag = html.match(new RegExp(`<[^>]*${selector}[^>]*>`))?.[0] ?? ""
  return tag.match(new RegExp(`\\s${name}="([^"]*)"`))?.[1]
}
const idOf = (html, text) => html.match(new RegExp(`<p id="([^"]+)"[^>]*>(?:<[^>]+>)*${text}`))?.[1]

describe("Field", () => {
  test("names its control: the label points at the id the control takes", () => {
    const html = renderToStaticMarkup(h(Field, { label: "Name" }, h(Probe)))
    const id = attr(html, "data-probe", "id")
    expect(id).toBeTruthy()
    expect(attr(html, 'data-slot="label"', "for")).toBe(id)
  })

  test("a caller's htmlFor is the label's, and the control keeps its own id", () => {
    const html = renderToStaticMarkup(
      h(Field, { label: "Name", htmlFor: "name" }, h(Probe, { id: "name" }), h(Probe, {})),
    )
    expect(attr(html, 'data-slot="label"', "for")).toBe("name")
    // A second control in the field does not take the first one's id.
    expect(html.match(/id="name"/g)).toHaveLength(1)
  })

  test("the hint describes the control, and the field is not invalid", () => {
    const html = renderToStaticMarkup(
      h(Field, { label: "Name", hint: "Letters and digits" }, h(Probe)),
    )
    const hint = idOf(html, "Letters and digits")
    expect(hint).toBeTruthy()
    expect(attr(html, "data-probe", "aria-describedby")).toBe(hint)
    expect(attr(html, "data-probe", "aria-invalid")).toBeUndefined()
  })

  test("an error replaces the hint, describes the control and makes it invalid", () => {
    const html = renderToStaticMarkup(
      h(Field, { label: "Name", hint: "Letters and digits", error: "Taken" }, h(Probe)),
    )
    expect(html).not.toContain("Letters and digits")
    const error = html.match(/<p id="([^"]+)" role="alert"/)?.[1]
    expect(error).toBeTruthy()
    expect(attr(html, "data-probe", "aria-describedby")).toBe(error)
    expect(attr(html, "data-probe", "aria-invalid")).toBe("true")
    // The error carries a glyph, so it is not told by its colour alone.
    expect(html).toMatch(/role="alert"[^>]*><svg[^>]*aria-hidden="true"/)
  })

  test("what the caller set comes first and wins", () => {
    const html = renderToStaticMarkup(
      h(
        Field,
        { label: "Name", hint: "Letters", error: undefined },
        h(Probe, { "aria-describedby": "own", "aria-invalid": false }),
      ),
    )
    const hint = idOf(html, "Letters")
    expect(attr(html, "data-probe", "aria-describedby")).toBe(`own ${hint}`)
    expect(attr(html, "data-probe", "aria-invalid")).toBe("false")
  })

  test("a group control is labelled by the label's id", () => {
    const html = renderToStaticMarkup(h(Field, { label: "Strategy" }, h(GroupProbe)))
    const labelId = attr(html, 'data-slot="label"', "id")
    expect(labelId).toBeTruthy()
    expect(attr(html, "radiogroup", "aria-labelledby")).toBe(labelId)
  })

  test("a control in the trailing slot is not the field's control", () => {
    const html = renderToStaticMarkup(
      h(
        Field,
        { label: "Name", error: "Taken", trailing: h(Probe, { "data-trailing": "" }) },
        h(Probe),
      ),
    )
    expect(attr(html, "data-trailing", "aria-invalid")).toBeUndefined()
    expect(attr(html, "data-trailing", "id")).toBeUndefined()
  })

  test("outside a field a control is left as it was", () => {
    const html = renderToStaticMarkup(h(Probe, { id: "alone" }))
    expect(html).toBe('<input data-probe="" id="alone"/>')
  })

  test("info is an Explain mark beside the label, not inside it", () => {
    const html = renderToStaticMarkup(h(Field, { label: "Name", info: "Why" }, h(Probe)))
    expect(html).toMatch(/<\/label><button[^>]*data-slot="explain"/)
    expect(attr(html, 'data-slot="explain"', "aria-label")).toBe("What this setting does")
  })
})

describe("Field group", () => {
  // A field of several controls, each named by its own words. Without
  // `group` every one of them took the field's id, and the label pointed at
  // the first: one id on many elements, and a `for` that named one control
  // of a set.
  const ids = (html) => [...html.matchAll(/\sid="([^"]+)"/g)].map((match) => match[1])
  const tags = (html, name) => html.match(new RegExp(`<${name}\\b[^>]*>`, "g")) ?? []
  const groupTag = (html) => html.match(/<div role="group"[^>]*>/)?.[0] ?? ""

  test("a role's settings, two inputs a setting: none takes the field's id, and the field names them as a group", () => {
    const html = renderToStaticMarkup(
      h(
        Field,
        { label: "Settings it carries", group: true, hint: "What current_setting reads" },
        h(Input, { "aria-label": "Setting 1 name" }),
        h(Input, { "aria-label": "Setting 1 value" }),
        h(Input, { "aria-label": "Setting 2 name" }),
        h(Input, { "aria-label": "Setting 2 value" }),
      ),
    )
    const inputs = tags(html, "input")
    expect(inputs).toHaveLength(4)
    for (const input of inputs) {
      expect(input).not.toMatch(/\sid=/)
      // Described once, by the group, rather than at every stop.
      expect(input).not.toContain("aria-describedby")
    }
    expect(new Set(ids(html)).size).toBe(ids(html).length)
    // A `for` cannot name a group; the group is named by the label's id.
    expect(attr(html, 'data-slot="label"', "for")).toBeUndefined()
    const label = attr(html, 'data-slot="label"', "id")
    const hint = idOf(html, "What current_setting reads")
    expect(groupTag(html)).toContain(`aria-labelledby="${label}"`)
    expect(groupTag(html)).toContain(`aria-describedby="${hint}"`)
  })

  test("a foreign key's columns, two selects a pair, are one group too", () => {
    const pair = (n) => [
      h(
        Select,
        { key: `own${n}`, value: "" },
        h(SelectTrigger, { "aria-label": `Column ${n} of orders` }, h(SelectValue)),
      ),
      h(
        Select,
        { key: `ref${n}`, value: "" },
        h(SelectTrigger, { "aria-label": `Column ${n} of customers` }, h(SelectValue)),
      ),
    ]
    const html = renderToStaticMarkup(
      h(Field, { label: "Columns", group: true }, ...pair(1), ...pair(2)),
    )
    const triggers = tags(html, "button").filter((tag) => tag.includes('role="combobox"'))
    expect(triggers).toHaveLength(4)
    for (const trigger of triggers) expect(trigger).not.toMatch(/\sid=/)
    expect(new Set(ids(html)).size).toBe(ids(html).length)
    expect(groupTag(html)).toContain(`aria-labelledby="${attr(html, 'data-slot="label"', "id")}"`)
  })

  test("an error describes the group and marks each of its controls invalid", () => {
    const html = renderToStaticMarkup(
      h(
        Field,
        { label: "Who may connect", group: true, hint: "First match decides", error: "Bad CIDR" },
        h(Input, { "aria-label": "Rule 1 source" }),
        h(Input, { "aria-label": "Rule 2 source" }),
      ),
    )
    const error = html.match(/<p id="([^"]+)" role="alert"/)?.[1]
    expect(groupTag(html)).toContain(`aria-describedby="${error}"`)
    for (const input of tags(html, "input")) expect(input).toContain('aria-invalid="true"')
  })

  test("a choice in the group still has its own name, and no second description", () => {
    const html = renderToStaticMarkup(
      h(
        Field,
        { label: "Which tables", group: true, hint: "Only those ticked" },
        h(Segments, {
          label: "Which tables the dump holds",
          value: "all",
          onChange() {},
          options: [
            { value: "all", label: "Every table" },
            { value: "only", label: "Only some" },
          ],
        }),
        h(Input, { "aria-label": "Filter the tables" }),
      ),
    )
    const radios = html.match(/<div[^>]*role="radiogroup"[^>]*>/)?.[0] ?? ""
    expect(radios).toContain('aria-label="Which tables the dump holds"')
    expect(radios).not.toContain("aria-describedby")
  })

  test("a field of one control is unchanged: it names the control, not a group", () => {
    const html = renderToStaticMarkup(h(Field, { label: "Name" }, h(Input, {})))
    expect(groupTag(html)).toBe("")
    expect(attr(html, 'data-slot="label"', "for")).toBe(
      tags(html, "input")[0].match(/\sid="([^"]+)"/)[1],
    )
  })
})

describe("FieldChecks", () => {
  test("is a polite live region, and each check says its state in words", () => {
    const html = renderToStaticMarkup(
      h(
        FieldChecks,
        null,
        h(FieldCheck, { met: true }, "12 characters or more"),
        h(FieldCheck, { met: false }, "a digit"),
      ),
    )
    expect(attr(html, "field-checks", "aria-live")).toBe("polite")
    expect(html).not.toContain('role="alert"')
    expect(html).toMatch(/12 characters or more<\/span><span class="sr-only">, met<\/span>/)
    expect(html).toMatch(/a digit<\/span><span class="sr-only">, not yet<\/span>/)
    // A change is read as the whole rule, not the bare word.
    expect(html.match(/aria-atomic="true"/g)).toHaveLength(2)
  })

  test("beside a field's control, it is one of the things the control is described by", () => {
    const html = renderToStaticMarkup(
      h(
        Field,
        { label: "Password", hint: "A long one" },
        h(Probe),
        h(FieldChecks, null, h(FieldCheck, { met: false }, "12 characters or more")),
      ),
    )
    const checks = attr(html, "field-checks", "id")
    const hint = idOf(html, "A long one")
    expect(checks).toBeTruthy()
    expect(attr(html, "data-probe", "aria-describedby")).toBe(`${hint} ${checks}`)
  })

  test("as a field's hint it is read as the hint, in a block a run of checks may sit in", () => {
    const html = renderToStaticMarkup(
      h(
        Field,
        {
          label: "Token",
          hint: h(FieldChecks, null, h(FieldCheck, { met: true }, "the bot's token")),
        },
        h(Probe),
      ),
    )
    const hint = html.match(/<div id="([^"]+)"[^>]*><span[^>]*field-checks/)?.[1]
    expect(hint).toBeTruthy()
    expect(attr(html, "data-probe", "aria-describedby")).toBe(hint)
  })
})
