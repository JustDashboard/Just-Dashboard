import { describe, expect, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Field } from "../form"
import { Checkbox } from "./checkbox"
import { Switch } from "./switch"
import { ToggleGroup, ToggleGroupItem } from "./toggle-group"

// Switch, Checkbox and ToggleGroup read the Field they sit in, as Input does.

const tagOf = (html, slot) => html.match(new RegExp(`<[^>]*data-slot="${slot}"[^>]*>`))?.[0] ?? ""
const attr = (tag, name) => tag.match(new RegExp(`\\s${name}="([^"]*)"`))?.[1]
const labelFor = (html) => attr(tagOf(html, "label"), "for")
const labelId = (html) => attr(tagOf(html, "label"), "id")

describe("Switch in a Field", () => {
  test("is the field's control: named by its label, described and invalid with its error", () => {
    const html = renderToStaticMarkup(
      h(Field, { label: "Spanning tree", error: "Not on a bond" }, h(Switch)),
    )
    const tag = tagOf(html, "switch")
    expect(attr(tag, "id")).toBe(labelFor(html))
    expect(attr(tag, "aria-describedby")).toBeTruthy()
    expect(attr(tag, "aria-invalid")).toBe("true")
  })

  test("a switch that names itself keeps the hint but not the field's id", () => {
    const html = renderToStaticMarkup(
      h(
        Field,
        { label: "Spanning tree", hint: "Where two switches meet" },
        h(Switch, { "aria-label": "On" }),
      ),
    )
    const tag = tagOf(html, "switch")
    expect(attr(tag, "id")).toBeUndefined()
    expect(attr(tag, "aria-describedby")).toBeTruthy()
  })

  test("outside a Field nothing is added", () => {
    const tag = tagOf(renderToStaticMarkup(h(Switch)), "switch")
    expect(attr(tag, "aria-describedby")).toBeUndefined()
    expect(attr(tag, "aria-invalid")).toBeUndefined()
  })
})

describe("Checkbox in a Field", () => {
  test("is described by the field but never takes its id", () => {
    const html = renderToStaticMarkup(
      h(Field, { label: "Tables", hint: "Only the ticked ones" }, h(Checkbox), h(Checkbox)),
    )
    const boxes = html.match(/<button[^>]*data-slot="checkbox"[^>]*>/g) ?? []
    expect(boxes).toHaveLength(2)
    for (const box of boxes) {
      expect(attr(box, "id")).toBeUndefined()
      expect(attr(box, "aria-describedby")).toBeTruthy()
    }
  })

  test("keeps the id it is given", () => {
    const tag = tagOf(
      renderToStaticMarkup(h(Field, { label: "Ops" }, h(Checkbox, { id: "ops" }))),
      "checkbox",
    )
    expect(attr(tag, "id")).toBe("ops")
  })
})

describe("ToggleGroup in a Field", () => {
  const group = (props) =>
    h(ToggleGroup, { type: "single", ...props }, h(ToggleGroupItem, { value: "a" }, "A"))

  test("is named by the field's label and described by its hint", () => {
    const html = renderToStaticMarkup(h(Field, { label: "Protocol", hint: "TCP or UDP" }, group()))
    const tag = tagOf(html, "toggle-group")
    expect(attr(tag, "aria-labelledby")).toBe(labelId(html))
    expect(attr(tag, "aria-describedby")).toBeTruthy()
  })

  test("a group the caller named keeps its name", () => {
    const html = renderToStaticMarkup(
      h(Field, { label: "Protocol" }, group({ "aria-label": "Mine" })),
    )
    const tag = tagOf(html, "toggle-group")
    expect(attr(tag, "aria-labelledby")).toBeUndefined()
    expect(attr(tag, "aria-label")).toBe("Mine")
  })
})
