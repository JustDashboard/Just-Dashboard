import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Field, FieldCheck, FieldChecks, useField, useFieldControl } from "./form"

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
